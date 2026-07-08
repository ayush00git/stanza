# Stanza — Molecule Generation

Claude **generates** chemistry: given the mutant resistance pocket and the WT→mutant delta,
it proposes drug-like SMILES aimed at binding the mutant while sparing the wild type.

One-line: the generative core that turns "here is the resistant pocket" into "here are
molecules that bind the mutant and spare the wild type."

---

## Implemented (current): propose on request, dock on demand

The shipped design **decouples proposal from docking**. Docking (Vina, ~30–60 s per molecule
× two tracks) is the entire cost, and blocking a generate call on it is what made the earlier
in-loop version take minutes. So the two steps are separate endpoints:

- **`POST /runs/:id/generate`** → one Claude call; returns the proposed SMILES immediately as
  `{ "run_id": "...", "candidates": ["<SMILES>", ...] }`. **No docking.** Body `{ "n": <int> }`
  is optional (how many to request, capped at `maxGenPerCall`). Runs Stage-3 pocket analysis
  first if it hasn't been done. New proposals are deduped against everything already docked or
  proposed and merged into `run.candidates`, so `GET /runs/:id` rehydrates them after a reload.
- **`POST /runs/:id/dock`** (Stage 4 — already synchronous and per-SMILES cached) → the
  frontend docks one molecule on demand when the user selects it and hits enter. Same
  list-then-dock UX as the ChEMBL fragment panel (`app/src/components/viewer/DockingPanel.tsx`):
  a fast list, then a per-row dock action.

**Feedback is user-driven, not autonomous.** Molecules already docked for a run are passed
back to Claude as scored history (`wt_score` / `mutant_score` / `selectivity`) on the next
`generate` call — dock a few, generate again, and the model climbs the gradient, without the
server ever running an unattended multi-round loop. The autonomous closed loop described below
(rounds, budgets, convergence, crash-resume) is the **target** design, not yet built.

---

## Goal

Produce a ranked set of candidate molecules that **bind the mutant pocket and spare the
wild type** — maximizing the selectivity margin (`wt_score − mutant_score`), not raw
affinity. A single run is an iterative search: each round Claude proposes structures, the
pipeline scores them on both tracks, and the scores (top and bottom performers) are folded
back into the next prompt so the model climbs the fitness gradient. The loop runs
**closed and single-threaded first** (correctness over throughput), then parallelizes each
compute step across workers.

This spec owns: the Go round orchestrator, the Claude call contract, what conditions the
prompt, the feedback strategy, the stop criteria, and the persisted state machine that lets
a crashed run resume. It **links out** for the compute steps — validation
([`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md)), dual-track docking
([`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md)), and
scoring/ranking ([`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md)).

---

## Current state

Claude now proposes molecules (`services/generation.go` → `GenerateCandidates`, wired to
`POST /runs/:id/generate`), and each is docked on demand through the dual-track dock (`04`).
The **autonomous** closed loop below — rounds, budgets, convergence, crash-resume — is not
built; what follows is its target design. The scaffolding it would orchestrate:

- **Molecules come from a fixed library.** `services/chembl.go` (`FetchFragments`) pulls
  fragment-like molecules from ChEMBL and ranks them against a single pocket's descriptors.
  This is the *thing being replaced* as the candidate source — the generation loop supplies
  molecules instead of a static library. ChEMBL may survive as an optional round-0 seed pool.
- **Async job pattern is proven.** `services/jobs.go` (`JobStore`: in-memory map keyed by
  UUID, background goroutine per job, `Submit`/`Get`, capped at `maxJobs`) plus
  `handlers/dock_handler.go` (`POST /dock` → `202 {job_id}`, `GET /dock/status?id=` polls)
  is the exact shape the generation run and its per-step jobs will reuse.
- **Docking works, single-track.** `services/docking.go` docks one SMILES into one pocket
  via Vina (`SMILESTo3D` → `PrepareReceptor`/`PrepareLigand` → `RunVinaDock` →
  `BindingAffinity`). This loop needs it run **twice per candidate** (mutant + WT); that
  dual-track wrapper and its cache are specified in `04`.
- **No durable state.** Jobs live only in memory; a process restart loses everything. The
  loop's state machine must be persisted (`08`) so a run resumes.

So this feature is genuinely new orchestration wired on top of the existing job/docking
primitives, with the Claude proposer as the one new external dependency.

---

## Target design: autonomous closed loop

> **Not yet built.** The sections below specify the *autonomous* loop, where the server runs
> propose → validate → dock → score → feedback for N rounds on its own until a budget or
> convergence stop. The shipped build (see *Implemented* above) instead exposes proposal and
> docking as separate on-demand endpoints, with feedback driven by the user re-calling
> `generate`. Keep this as the north star for when unattended, hands-off runs are wanted.

### One round

```
round r  (state transitions in [brackets])

  ┌─ PROPOSE ───────────────────────────────────────────────────┐
  │ Claude( mutant_pocket + pocket_delta + curated history, n=N )│ → N SMILES   [draft]
  └──────────────────────────────────────────────────────────────┘
        │
        ▼  queue: validate jobs   → 05 (RDKit)
  drop invalid parse / duplicates (canonical SMILES) / non-drug-like  [validated]
        │
        ▼  queue: dock jobs       → 06 (dual-track Vina, idempotent cache)
  dock each survivor into the mutant pocket AND the WT pocket
  → mutant_score, wt_score                                            [docked]
        │
        ▼  score                  → 07
  selectivity = wt_score − mutant_score
  fitness     = f(mutant_potency, selectivity, drug_likeness)         [scored]
        │
        ▼  orchestrator decision (runs in Go)
  budget remaining AND still improving?
        ├─ yes → build feedback (best + worst) → round r+1            [next_round]
        └─ no  → freeze leaderboard, finalize run                     [done]
```

The **round loop runs in Go** and owns the continue/stop decision. Each compute step
(validate, dock, score) is dispatched as a **queued job for workers** — the orchestrator
fans candidates out, waits for the batch, then advances state. **Cap `N` candidates per
round** so a round's cost is bounded regardless of how many the model returns.

### The Claude call — structured in, SMILES out, no prose

The proposer is a single structured request. Discipline:

- **Latest Claude model.** Default `claude-opus-4-8` via the Anthropic Go SDK
  (`github.com/anthropics/anthropic-sdk-go`, `anthropic.ModelClaudeOpus4_8`). The model id
  is config so it can move to a newer model (or Claude Fable 5 for the hardest exploration)
  without touching the loop. Use **adaptive thinking** (`thinking: {type: "adaptive"}`) —
  the model decides its own reasoning depth per round.
- **Structured JSON in, structured JSON out.** The request carries the input contract
  below; the response is constrained to `{ candidates: ["<SMILES>", ...] }` and nothing
  else. Enforce this with **structured outputs** (`output_config.format` = a `json_schema`
  for the candidates array) *or* a single **strict tool** (`strict: true`) whose input
  schema is the output contract. Either guarantees a parseable, SMILES-only response.
- **SMILES only — no prose.** No explanations, no numbering, no markdown. Every array
  element is one raw SMILES string. The system prompt states the task (propose `n`
  drug-like molecules that bind the mutant pocket and avoid the WT pocket, learning from the
  scored history); the input JSON carries the conditioning; the schema forbids anything but
  SMILES. Parse with `json.Unmarshal` — never string-scrape the response.
- **Robust to junk.** Even under a strict schema, treat the output as untrusted: dedupe,
  and let `05` reject anything unparseable. A malformed or empty response fails the round's
  propose job (retry once, then surface an error on the run) rather than crashing the loop.

### What conditions the prompt

The proposer sees only what it needs to reason about selectivity:

- **Mutant pocket facts** (from `03`): `key_residues`, `volume`, `hydrophobicity`. These
  tell the model the shape and chemistry of the site it must bind.
- **Pocket delta** (from `03`): `changed` (what the mutation did to the pocket, e.g. a
  gatekeeper substitution narrowing the back cleft) and `effect` (the consequence for
  binding, e.g. a lost H-bond donor or added steric bulk). This is the resistance signal —
  it tells the model *why* the wild-type binder fails and what to exploit.
- **Scored history** (from `07`): prior candidates as `{smiles, mutant_score, wt_score,
  qed}`. This is the gradient — concrete evidence of what improved or hurt the margin.

The WT pocket itself is **not** sent to the model as geometry — the loop encodes "spare the
WT" through the *scores* in history (both `mutant_score` and `wt_score`), which is the
signal the model actually optimizes against.

### Feedback strategy — surface the gradient, both ends

After scoring, the orchestrator curates which history entries go back next round. Naively
sending only the winners teaches the model *what works* but not *what to avoid*. Instead
**send the top-k by fitness AND the bottom-k by fitness**, so the model sees the contrast:

- **Top performers** — high selectivity margin, potent on the mutant, drug-like. "More like
  these."
- **Bottom performers** — negative/low margin (bound the WT as well or better), weak, or
  non-drug-like. "Not like these, and here's the number that says why."

History is bounded (the `history` array is capped, budget-shared between best and worst) so
the prompt stays small and cacheable. Because the entries carry both `mutant_score` and
`wt_score`, the model can derive the margin (`wt_score − mutant_score`) directly and learn
which structural moves widen it. Round 0 has no history; the array is empty (optionally
seeded with a few ChEMBL fragments from `services/chembl.go` as cold-start exemplars).

### Stop criteria

The orchestrator stops the run when **any** of:

- **Round budget** — `round >= MaxRounds`.
- **Dock budget** — cumulative docks `>= MaxDocks`. Each candidate costs **2 docks**
  (mutant + WT), so this is the real compute cap; the cache in `04` means re-proposed,
  already-docked SMILES do not re-spend budget.
- **Convergence** — best fitness has not improved by more than `epsilon` over the last
  `ConvergenceK` consecutive rounds (a stall counter). This catches the loop plateauing and
  avoids burning budget on rounds that no longer climb.

On stop, the run finalizes: freeze the pooled, ranked leaderboard (`07`) and mark the run
`done`.

### Persisted state machine

Each round is a state machine, persisted after every transition so a crashed run resumes
from the last completed state rather than restarting:

```
draft ─▶ validated ─▶ docked ─▶ scored ─▶ next_round ──▶ (draft of r+1)
                                     └────▶ done
```

- `draft` — Claude proposals received (or being requested); not yet validated.
- `validated` — survived `05` (parse + dedupe + drug-likeness).
- `docked` — both tracks docked via `04`; `mutant_score`/`wt_score` present.
- `scored` — fitness + selectivity computed by `07`.
- `next_round` — decision: budget remains and still improving → advance round.
- `done` — decision: stopped; leaderboard frozen.

Persistence and the worker queue are owned by
[`08-persistence-and-queue.md`](08-persistence-and-queue.md): the run row (config, current
`state`, `round`, cumulative `docks`, best-fitness, stall counter) and every candidate
(SMILES, round, scores, validity) are written on each transition. On restart, the
orchestrator loads the run, reads `state`, and re-enters the loop at that point — an
interrupted `docked` round re-scores rather than re-docks (docks are cached in `04`), an
interrupted `draft` re-requests proposals. This loop's state machine is a **sub-state of
the overall run lifecycle** in
[`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md); the generation run
advances the parent run through its "generating" phase.

---

## Contracts

### Claude call — input (Go → model)

```json
{
  "mutant_pocket": {
    "key_residues": ["ILE315", "PHE317", "MET318"],
    "volume": 412.5,
    "hydrophobicity": 0.62
  },
  "pocket_delta": {
    "changed": "T315I gatekeeper substitution narrows the back cleft",
    "effect": "removes a hinge H-bond donor and adds steric bulk"
  },
  "history": [
    { "smiles": "Cc1ccc(cc1)C(=O)Nc1ccccc1", "mutant_score": -9.8, "wt_score": -7.1, "qed": 0.74 },
    { "smiles": "O=C(Nc1ccccc1)c1ccncc1",    "mutant_score": -6.2, "wt_score": -8.9, "qed": 0.55 }
  ],
  "n": 12
}
```

`history` is curated: top-k + bottom-k by fitness, capped. `n` = candidates requested this
round (≤ `NPerRound`). Sign convention (shared with `04`/`07`): Vina affinities are
kcal/mol, **more negative = tighter**; `selectivity = wt_score − mutant_score`, **positive
and large = binds mutant, spares WT**.

### Claude call — output (model → Go)

```json
{ "candidates": ["Cc1ccc(cc1)C(=O)Nc1ncc(F)cc1", "COc1ccc(cc1)C(=O)Nc1cccnc1"] }
```

SMILES strings only. No prose, no keys other than `candidates`. Enforced by
`output_config.format` (json_schema) or a strict single tool.

### Go — request/response types (`services/generation`)

```go
type PocketFacts struct {
    KeyResidues   []string `json:"key_residues"`
    Volume        float64  `json:"volume"`
    Hydrophobicity float64 `json:"hydrophobicity"`
}

type PocketDelta struct {
    Changed string `json:"changed"` // what the mutation did to the pocket (from 03)
    Effect  string `json:"effect"`  // consequence for binding (from 03)
}

type HistoryEntry struct {
    SMILES      string  `json:"smiles"`
    MutantScore float64 `json:"mutant_score"`
    WTScore     float64 `json:"wt_score"`
    QED         float64 `json:"qed"`
}

type ClaudeInput struct {
    MutantPocket PocketFacts    `json:"mutant_pocket"`
    PocketDelta  PocketDelta    `json:"pocket_delta"`
    History      []HistoryEntry `json:"history"`
    N            int            `json:"n"`
}

type ClaudeOutput struct {
    Candidates []string `json:"candidates"`
}

// Propose calls Claude (claude-opus-4-8, adaptive thinking, structured output)
// and returns SMILES-only candidates. model is config-injected.
func Propose(ctx context.Context, in ClaudeInput, model string) (ClaudeOutput, error)
```

### Go — round state enum

```go
type RoundState string

const (
    StateDraft     RoundState = "draft"      // proposals in hand, pre-validation
    StateValidated RoundState = "validated"  // survived RDKit (05)
    StateDocked    RoundState = "docked"     // dual-track docked (04)
    StateScored    RoundState = "scored"     // fitness computed (07)
    StateNextRound RoundState = "next_round" // decision: continue
    StateDone      RoundState = "done"       // decision: stop
)
```

### Go — orchestrator & candidate

```go
type GenerationConfig struct {
    RunID         string
    MutantPocket  PocketFacts
    PocketDelta   PocketDelta
    MutantPDBPath string        // receptor for the mutant dock track (04)
    WTPDBPath     string        // receptor for the WT dock track (04)
    NPerRound     int           // CAP N candidates per round
    MaxRounds     int           // round budget
    MaxDocks      int           // dock budget (2 docks / candidate)
    ConvergenceK  int           // stop after k rounds with no fitness gain
    Epsilon       float64       // minimum fitness gain that counts as improvement
    Model         string        // e.g. "claude-opus-4-8"
}

type Candidate struct {
    SMILES      string     `json:"smiles"`
    Round       int        `json:"round"`
    Valid       bool       `json:"valid"`        // from 05
    QED         float64    `json:"qed"`          // from 05
    MutantScore float64    `json:"mutant_score"` // from 06
    WTScore     float64    `json:"wt_score"`     // from 06
    Selectivity float64    `json:"selectivity"`  // wt_score - mutant_score (07)
    Fitness     float64    `json:"fitness"`      // f(potency, selectivity, drug-likeness) (07)
}

type GenerationRun struct {
    Cfg   GenerationConfig
    State RoundState
    Round int
    Docks int         // cumulative docks spent
    Pool  []Candidate // every scored candidate across rounds
    Best  float64     // best fitness observed
    Stall int         // rounds since Best last improved
}

// Loop drives the run: propose → validate(05) → dock(04) → score(07) → decide,
// persisting after each transition. Returns when the run reaches StateDone.
func (r *GenerationRun) Loop(ctx context.Context) error

// Step advances exactly one state transition (dispatch + await one batch of
// queued jobs, or make the continue/stop decision). Used by Loop and by resume.
func (r *GenerationRun) Step(ctx context.Context) error

// Feedback returns the curated history for the next round: top-k + bottom-k of
// Pool by fitness, capped to the history budget.
func (r *GenerationRun) Feedback() []HistoryEntry

// ShouldStop reports whether any stop criterion fires, with a human reason
// ("round_budget" | "dock_budget" | "converged").
func (r *GenerationRun) ShouldStop() (bool, string)
```

### Go — run store & HTTP surface (mirrors `jobs.go` / `dock_handler.go`)

```go
// GenerationStore tracks runs; same in-memory + background-goroutine shape as
// JobStore, but each run is persisted (08) so it survives restart.
func (s *GenerationStore) Submit(cfg GenerationConfig) string          // → run_id, background Loop
func (s *GenerationStore) Get(runID string) (GenerationStatus, bool)   // status + current leaderboard
```

- `POST /generate` — body: pocket ids + budgets; validates, enqueues a run, responds
  `202 {"run_id": "..."}`.
- `GET /generate/status?id=<runID>` — returns run `state`, `round`, `docks`, best fitness,
  stop reason (if done), and the current ranked leaderboard (`07`).

---

## Dependencies & touch points

| Direction | Feature | Relationship |
|---|---|---|
| **in** | [`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md) | Generation is a sub-phase of the run; the loop's state machine advances the parent run's "generating" state. |
| **in** | [`03-dual-pocket-analysis-and-delta.md`](03-dual-pocket-analysis-and-delta.md) | Supplies `mutant_pocket` facts (key residues, volume, hydrophobicity) and `pocket_delta` (changed/effect) that condition the prompt. |
| **calls** | [`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md) | Each round's `draft → validated`: parse, dedupe (canonical SMILES), drug-likeness / QED. |
| **calls** | [`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md) | Each round's `validated → docked`: dock every survivor into mutant AND WT pockets; idempotent cache so re-proposed SMILES do not re-dock or re-spend budget. |
| **calls** | [`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md) | Each round's `docked → scored`: selectivity margin + fitness; provides the ranking that feeds `Feedback()` and the final leaderboard. |
| **in** | [`08-persistence-and-queue.md`](08-persistence-and-queue.md) | Persists run + candidate state per transition (crash-resume); provides the worker queue that runs each compute step. |
| **reuses** | `services/jobs.go`, `handlers/dock_handler.go` | Async job pattern (UUID keys, background goroutine, `202` + poll) for the run store and per-step jobs. |
| **replaces** | `services/chembl.go` | As the candidate source. May survive as an optional round-0 seed pool / cold-start exemplars. |
| **new dep** | Anthropic Go SDK | `github.com/anthropics/anthropic-sdk-go`; `ANTHROPIC_API_KEY` in env. First Claude integration in the project. |

---

## Acceptance criteria

1. **Closed loop runs end to end.** `POST /generate` starts a run that executes N rounds of
   propose → validate → dock(both tracks) → score → feedback, and `GET /generate/status`
   reflects live state, round, dock count, and the current leaderboard.
2. **Claude proposer is structured and SMILES-only.** Requests use `claude-opus-4-8`
   (config-swappable) with adaptive thinking; responses are constrained to
   `{candidates: [...]}` via structured output or a strict tool; parsing never scrapes
   prose. A malformed response fails the propose job (retry once) without crashing the loop.
3. **Conditioning is wired.** The prompt carries mutant pocket facts + pocket delta (from
   `03`) and the curated scored history (from `07`); round 0 sends empty history.
4. **Feedback surfaces both ends.** The history sent each round contains top-k *and*
   bottom-k performers by fitness, within a capped budget — verifiably not winners-only.
5. **Budgets and convergence are enforced.** A run stops on round budget, dock budget
   (counting 2 docks/candidate, cache-aware), or `ConvergenceK` stalled rounds — with the
   stop reason recorded.
6. **Per-round cap holds.** No round docks more than `NPerRound` candidates regardless of
   how many SMILES the model returns.
7. **Crash-resume works.** Killing the process mid-run and restarting resumes from the last
   persisted state (e.g. a `docked` round re-scores rather than re-docks); no round is fully
   re-run from scratch, no dock budget is double-counted.
8. **Selectivity is the objective.** The final leaderboard ranks by fitness driven by the
   selectivity margin (`wt_score − mutant_score`), not by raw mutant affinity alone.

---

## Open questions / risks

- **Prompt caching vs. changing history.** History changes every round, which sits at the
  end of the prompt; keep the stable system prompt + pocket facts first so the cacheable
  prefix survives, and only the small curated-history tail varies. Confirm the history cap
  keeps the prefix worth caching.
- **Duplicate / mode-collapse candidates.** The model may re-propose near-identical winners
  round after round, wasting the `N` cap. Dedupe on canonical SMILES (`05`) *and* consider a
  novelty/diversity nudge in the prompt or a similarity filter before docking. Cache in `04`
  softens the compute cost but not the wasted proposal slots.
- **Fitness weighting is deferred to `07`.** The exact `f(potency, selectivity,
  drug_likeness)` (weights, whether potency uses `−mutant_score`, how QED enters) lives in
  `07`; this loop only consumes the scalar. A poorly weighted fitness makes the feedback
  gradient misleading — validate the weighting jointly with this loop.
- **Convergence tuning.** `epsilon` and `ConvergenceK` trade compute against thoroughness;
  too tight stops early on a plateau that would have broken through, too loose burns budget.
  Needs empirical tuning per target.
- **Docking noise vs. margin.** Vina affinities carry run-to-run variance that can rival a
  small selectivity margin; a "win" may be noise. Consider multiple poses / averaged
  affinity (a `04` concern) before trusting a narrow margin, and surface margin uncertainty
  to the model rather than a single number.
- **Model / API failure modes.** Rate limits, refusals, or timeouts on the proposer must
  degrade gracefully (retry with backoff, then fail the run with a clear status) — the
  round loop must never wedge waiting on the API.
- **Cost ceiling.** Rounds × candidates × 2 docks × model calls can grow fast; the dock and
  round budgets are the guardrails, but a per-run cost estimate surfaced at `POST /generate`
  would help callers size a run before spending.
- **Where scoring runs.** `05` (RDKit) and `07` are Python-flavored steps while today's
  compute shells out from Go directly; whether they become queued Python workers or Go
  shell-outs is an `08`/`05`/`07` decision that affects how this orchestrator dispatches
  each step.
