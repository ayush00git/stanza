import { useState } from 'react'
import {
  dropReasonLabel,
  fetchRunChembl,
  type Candidate,
  type Fragment,
  type GenerateProgress,
  type MoleculeCheck,
  type ValidationSummary,
} from '../../lib/api'
import { Steps, GEN_STEPS, genStepIndex } from '../Thinking'

/** Per-candidate docking phase, keyed by SMILES in the page's dockState map. */
export type CandidatePhase = 'docking' | 'done' | 'error'
export type CandidateDockState = {
  phase: CandidatePhase
  selectivity?: number
  error?: string
}

type Props = {
  /** The run id, for fetching ChEMBL reference molecules for this pocket. */
  runId: string
  candidates: Candidate[]
  generating: boolean
  generateError?: string | null
  /** The pre-filter's verdict on the most recent round; null before any round runs. */
  validation?: ValidationSummary | null
  /** The live generation round: current step, and each molecule's verdict as it lands. */
  genProgress?: GenerateProgress | null
  genChecks?: MoleculeCheck[]
  onGenerate: (n: number) => void
  /** Docking state keyed by candidate SMILES. */
  dockState: Record<string, CandidateDockState>
  onDock: (smiles: string, source?: 'claude' | 'chembl') => void
  /** False while the run isn't ready to generate (e.g. no mutant structure). */
  canGenerate: boolean
  /** The reactive residue, e.g. "Cys12", for the feedback-round copy. */
  covalentResidue?: string | null
}

const GEN_COUNTS = [4, 6, 8]

/** Truncate a SMILES string for compact display. */

/** Signed selectivity, e.g. "+4.10" / "−0.30" (true minus glyph). */
function signedSel(x: number): string {
  const s = x.toFixed(2)
  return x > 0 ? `+${s}` : s.replace('-', '−')
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <span className="inline-flex items-baseline gap-1 text-xs text-muted">
      <span className="tabular-nums text-ink">{value}</span>
      <span>{label}</span>
    </span>
  )
}

/**
 * The generation round, live.
 *
 * Claude takes a minute or two to design against the pocket, and the pre-filter then
 * discards some of what it returns. Both used to happen behind a spinner, so a request for
 * 8 molecules resolving to 2 looked like a failure. Here the stages narrate themselves and
 * each SMILES is shown the moment its verdict lands — kept, or dropped with the reason.
 */
function GenerationStream({
  progress,
  checks,
}: {
  progress: GenerateProgress | null
  checks: MoleculeCheck[]
}) {
  const pct =
    progress && progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : null
  const kept = checks.filter((c) => c.kept).length
  // The stream reports a real stage; map it onto the stacked step list so the steps
  // advance with the server, not on a timer.
  const stepIdx = genStepIndex(progress?.stage ?? '')

  return (
    <div className="mt-3 rounded-md border border-hairline bg-paper-deep/40 px-3 py-2.5" aria-live="polite">
      {/* Sequential steps in Claude's terracotta, driven by the stream's stage. */}
      <Steps phases={GEN_STEPS} activeIndex={stepIdx} />

      {/* The server's own status line under the steps: its message, and the per-molecule
          count once screening starts. */}
      <p className="mt-2.5 text-xs text-muted">
        {progress?.message ?? 'starting'}
        {progress && progress.total > 0 && ` · ${progress.done} of ${progress.total}`}
      </p>

      <div className="mt-2 h-0.5 overflow-hidden rounded-full bg-hairline">
        <div
          className="h-full rounded-full bg-claude transition-[width] duration-500"
          style={{ width: `${pct ?? 4}%` }}
        />
      </div>

      {checks.length > 0 && (
        <>
          <p className="mt-2.5 text-xs text-muted">
            <span className="tabular-nums text-ink">{kept}</span> kept of{' '}
            <span className="tabular-nums text-ink">{checks.length}</span> checked
          </p>
          <ul className="mt-1.5 space-y-1.5 border-t border-hairline pt-2">
            {checks.map((c, i) => (
              <li key={`${c.smiles}-${i}`} className="flex flex-col gap-0.5">
                <span className="break-all font-mono text-[11px] leading-snug text-muted">{c.smiles}</span>
                <span className={`text-xs ${c.kept ? 'text-accent' : 'text-ink'}`}>
                  {c.kept
                    ? `kept · ${c.candidate?.mol_weight.toFixed(0) ?? '?'} Da`
                    : `dropped — ${c.reason ?? 'unknown'}`}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  )
}

/** One node in the feedback-loop diagram. */
function LoopStep({ n, label }: { n: number; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-accent/30 bg-paper px-2.5 py-1">
      <span className="flex h-4 w-4 items-center justify-center rounded-full bg-accent text-[9px] font-semibold text-paper">
        {n}
      </span>
      <span className="whitespace-nowrap text-ink">{label}</span>
    </span>
  )
}

/**
 * The generate → dock → feed-back iteration, drawn.
 *
 * This is the pipeline's headline capability and the earlier one-liner buried it. The
 * three steps run left to right; a dashed CSS bracket carries the docked results back
 * under them to the start, arrowhead into Generate. Nothing here is automatic — the
 * bracket is the DATA path, and the user acts on it by hitting Generate. The copy says so.
 * Rendered unconditionally: it explains the mechanism, so it should not wait for the first
 * dock to appear.
 *
 * It states the MECHANISM (what is fed back, and how it is ranked) and the INTENT (aiming
 * for stronger molecules), never a proven outcome — whether the next batch actually scores
 * higher is measured on the leaderboard. A claim of improvement the iteration may not
 * deliver at this sample size would be the exact overclaim this project removes elsewhere.
 */
function FeedbackLoop({ covalentResidue }: { covalentResidue?: string | null }) {
  const reachLabel = covalentResidue ? `Reach ${covalentResidue}` : 'Score selectivity'
  const rankedBy = covalentResidue
    ? `which warhead reached ${covalentResidue}`
    : 'measured selectivity'

  return (
    <div className="mt-3 rounded-md border border-accent/40 bg-accent-soft px-3.5 py-3">
      <div className="flex items-center gap-1.5 text-xs font-medium text-accent">
        <svg viewBox="0 0 16 16" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.6">
          <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9" strokeLinecap="round" />
          <path d="M12.4 1.8v2.9h-2.9" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        Feedback round
      </div>

      {/* The cycle: 1 → 2 → 3 forward, then a dashed arc 3 → 1 back to the start. The whole
          diagram is width-capped (max-w) so the return arc keeps an arc's proportions —
          left unconstrained it stretched to the full panel and blew the arrowhead up into a
          clunky smear. The arc is the data path (docked results), dashed to read as carried
          back rather than run automatically; the user closes it by clicking Generate. */}
      <div className="mt-2.5 max-w-sm">
        <div className="flex items-center gap-1.5 text-[11px]">
          <LoopStep n={1} label="Generate" />
          <span className="text-accent">→</span>
          <LoopStep n={2} label="Dock" />
          <span className="text-accent">→</span>
          <LoopStep n={3} label={reachLabel} />
        </div>
        <svg viewBox="0 0 340 30" className="mt-0.5 w-full text-accent" fill="none" aria-hidden="true">
          <path
            d="M328 6 Q170 40 14 6"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeDasharray="5 3"
            strokeLinecap="round"
          />
          {/* filled arrowhead into step 1 (bottom-left), pointing up-left */}
          <path d="M14 6 L22 3 L20 12 Z" fill="currentColor" />
        </svg>
      </div>

      <p className="mt-1.5 text-xs leading-relaxed text-ink">
        <span className="font-medium">Every molecule you dock is fed back to Claude.</span>{' '}
        Hit Generate and it re-designs against {rankedBy}, aiming for stronger molecules each
        round.
      </p>
    </div>
  )
}

/**
 * What the Stage-5 pre-filter did to the round that just ran. Claude proposing 8
 * molecules and the board showing 2 looks like a generation failure; it usually is not.
 * The filter is the quietest stage in the pipeline and the one most likely to be
 * misconfigured — it silently deleted every clinical KRAS G12C inhibitor until the
 * curated weight window was wired through. A drop nobody sees is a drop nobody audits.
 */
function ValidationNote({ v }: { v: ValidationSummary }) {
  const dropped = v.proposed - v.kept
  if (dropped <= 0) {
    return (
      <p className="mt-3 text-xs text-muted">
        Claude proposed {v.proposed}; the pre-filter kept all of them.
      </p>
    )
  }
  return (
    <details className="mt-3 rounded-md border border-hairline bg-paper-deep/40 px-3 py-2">
      <summary className="cursor-pointer list-none text-xs text-muted marker:content-none">
        <span className="text-ink tabular-nums">{v.proposed}</span> proposed ·{' '}
        <span className="text-ink tabular-nums">{v.kept}</span> kept ·{' '}
        <span className="text-ink tabular-nums">{dropped}</span> dropped by the pre-filter
        <span className="ml-1.5 text-muted">— why?</span>
      </summary>
      <ul className="mt-2 space-y-1.5 border-t border-hairline pt-2">
        {(v.details ?? []).map((d, i) => (
          <li key={`${d.smiles}-${i}`} className="flex flex-col gap-0.5">
            <span className="break-all font-mono text-[11px] leading-snug text-muted">{d.smiles}</span>
            <span className="text-xs text-ink">{dropReasonLabel(d, v)}</span>
          </li>
        ))}
      </ul>
      {v.mw_min != null && v.mw_max != null && (
        <p className="mt-2 border-t border-hairline pt-2 text-[11px] leading-relaxed text-muted">
          The window ({v.mw_min}–{v.mw_max} Da) comes from this target's curated site, not
          from the default rule-of-five ceiling — the generation prompt asks for molecules
          in that range, so the filter has to admit them.
        </p>
      )}
    </details>
  )
}

/**
 * Known ChEMBL molecules docked as a REFERENCE, not as candidates.
 *
 * Everything else on this board is Claude's output. This block lets a user dock a
 * published compound through the exact same dual-track + covalent pipeline, so a real
 * drug and a novel scaffold are scored on one ruler. It answers the board's hardest
 * question — is the geometry gate calibrated, or does nothing clear it? — by putting a
 * molecule with a known answer next to the generated ones. These bypass the 430–620 Da
 * generation gate on purpose: that gate steers what Claude proposes, not what you choose
 * to dock as a control.
 */
function ChemblReference({
  fragments,
  dockState,
  onDock,
}: {
  fragments: Fragment[]
  dockState: Record<string, CandidateDockState>
  onDock: (smiles: string, source?: 'claude' | 'chembl') => void
}) {
  if (fragments.length === 0) return null
  return (
    <div className="mt-4">
      <div className="flex items-baseline gap-2">
        <span className="text-xs font-medium text-ink">Reference · ChEMBL</span>
        <span className="text-xs text-muted">known compounds sized to this pocket — dock as a control</span>
      </div>
      <ul className="mt-2 max-h-[22rem] overflow-y-auto rounded-md border border-hairline bg-paper">
        {fragments.map((f) => {
          const state = dockState[f.smiles]
          const busy = state?.phase === 'docking'
          return (
            <li key={f.chembl_id} className="border-b border-hairline p-3 last:border-b-0">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex items-baseline gap-2">
                    <span className="text-xs font-medium text-ink">{f.name || f.chembl_id}</span>
                    <span className="text-[11px] text-muted">{f.chembl_id}</span>
                  </div>
                  <p className="mt-0.5 break-all font-mono text-[11px] leading-snug text-muted">{f.smiles}</p>
                  <div className="mt-1 flex gap-3">
                    <Metric label="MW" value={f.mol_weight.toFixed(0)} />
                    <Metric label="logP" value={f.logp.toFixed(2)} />
                  </div>
                </div>
                <div className="flex flex-none flex-col items-end gap-1.5">
                  {state ? (
                    <DockBadge state={state} />
                  ) : (
                    <button
                      type="button"
                      onClick={() => onDock(f.smiles, 'chembl')}
                      disabled={busy}
                      className="rounded-md border border-hairline px-3 py-1.5 text-xs text-ink transition-colors hover:border-ink disabled:opacity-50"
                    >
                      Dock
                    </button>
                  )}
                </div>
              </div>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

function DockBadge({ state }: { state: CandidateDockState }) {
  if (state.phase === 'docking') {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-hairline px-2 py-0.5 text-xs text-muted">
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-current" />
        Docking
      </span>
    )
  }
  if (state.phase === 'error') {
    return (
      <span className="rounded-full bg-conf-verylow/15 px-2 py-0.5 text-xs text-ink">Failed</span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-accent-soft px-2 py-0.5 text-xs text-accent">
      selectivity{' '}
      <span className="tabular-nums">{state.selectivity != null ? signedSel(state.selectivity) : '—'}</span>
    </span>
  )
}

/**
 * CandidatePanel — the generative counterpart to the ChEMBL DockingPanel. A
 * "Generate" control asks Claude for drug-like molecules aimed at the mutant
 * pocket (already RDKit-filtered server-side); each returned candidate lists its
 * drug-likeness numbers and a Dock button that docks it into BOTH tracks at once.
 * Docking is synchronous server-side, so a click resolves straight to a result —
 * no polling. Presentation only; the page owns generate/dock lifecycle + state.
 */
export default function CandidatePanel({
  runId,
  candidates,
  generating,
  generateError,
  validation,
  genProgress,
  genChecks = [],
  onGenerate,
  dockState,
  onDock,
  canGenerate,
  covalentResidue,
}: Props) {
  const [n, setN] = useState(6)
  const [chembl, setChembl] = useState<Fragment[]>([])
  const [chemblLoading, setChemblLoading] = useState(false)
  const [chemblError, setChemblError] = useState<string | null>(null)

  const handleFetchChembl = () => {
    setChemblLoading(true)
    setChemblError(null)
    fetchRunChembl(runId)
      .then((frags) => {
        setChembl(frags)
        if (frags.length === 0) setChemblError('No ChEMBL molecules matched this pocket.')
      })
      .catch((e: unknown) => setChemblError(e instanceof Error ? e.message : 'ChEMBL fetch failed'))
      .finally(() => setChemblLoading(false))
  }

  return (
    <div>
      {/* Generate control. */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-sm font-medium text-ink">Candidate molecules</span>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={handleFetchChembl}
            disabled={chemblLoading || !canGenerate}
            className="rounded-md border border-hairline px-3 py-1.5 text-xs text-muted transition-colors hover:border-ink hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
            title="Fetch known ChEMBL molecules sized to this pocket, to dock as a reference alongside Claude's proposals"
          >
            {chemblLoading ? 'Fetching…' : 'Fetch from ChEMBL'}
          </button>
          <div className="flex rounded-md border border-hairline bg-paper-deep p-0.5">
            {GEN_COUNTS.map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => setN(c)}
                disabled={generating}
                className={`rounded px-2.5 py-1 text-xs tabular-nums transition-colors disabled:opacity-50 ${
                  n === c ? 'bg-paper text-ink shadow-[0_1px_2px_rgba(18,22,28,0.12)]' : 'text-muted hover:text-ink'
                }`}
              >
                {c}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => onGenerate(n)}
            disabled={generating || !canGenerate}
            className="rounded-md border border-ink bg-ink px-3.5 py-1.5 text-xs font-medium text-paper transition-colors hover:bg-transparent hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
          >
            {generating ? 'Generating…' : 'Generate From Claude'}
          </button>
        </div>
      </div>

      {chemblError && <p className="mt-3 text-xs text-muted">{chemblError}</p>}
      <ChemblReference fragments={chembl} dockState={dockState} onDock={onDock} />

      {/* Once molecules have been docked, regeneration is a feedback round: every docked
          result (ranked by covalent feasibility) travels back into the design prompt, so
          Claude sees which warheads actually reached the thiol and which missed. This is
          the headline capability, so it is highlighted rather than muted. It states the
          MECHANISM — what is fed back — not an outcome: whether the next batch scores
          higher is a measured question, and the leaderboard is where it is read. */}
      {!generating && <FeedbackLoop covalentResidue={covalentResidue} />}

      {generateError && <p className="mt-3 text-sm text-conf-verylow">{generateError}</p>}

      {!generating && validation && <ValidationNote v={validation} />}

      {/* Regenerating over an existing list: the list stays put, so the live round has to
          be narrated up here or it would not be seen at all. */}
      {generating && <GenerationStream progress={genProgress ?? null} checks={genChecks} />}

      {candidates.length === 0 ? (
        <div className="mt-4 rounded-lg border border-dashed border-hairline bg-paper-deep/40 px-6 py-12 text-center">
          {!generating && (
            <p className="text-sm text-muted">
              {canGenerate
                ? 'Generate molecules to design against the mutant pocket'
                : 'Run needs a mutant structure before generating'}
            </p>
          )}
        </div>
      ) : (
        <ul className="mt-4 max-h-[28rem] overflow-y-auto rounded-md border border-hairline bg-paper">
          {candidates.map((c) => {
            const state = dockState[c.smiles]
            const busy = state?.phase === 'docking'
            return (
              <li
                key={c.smiles}
                className="flex flex-col gap-3 border-b border-hairline px-3 py-3 last:border-b-0 sm:flex-row sm:items-start sm:justify-between"
              >
                <div className="min-w-0 flex-1">
                  {/* Never truncated: the SMILES is the molecule's identity, and an ellipsis
                      makes two different candidates look alike. */}
                  <p className="break-all font-mono text-xs leading-relaxed text-ink">{c.smiles}</p>
                  <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1">
                    <Metric label="QED" value={c.qed.toFixed(2)} />
                    <Metric label="MW" value={c.mol_weight.toFixed(0)} />
                    <Metric label="logP" value={c.logp.toFixed(2)} />
                    {c.sa_score != null && <Metric label="SA" value={c.sa_score.toFixed(1)} />}
                    {c.ro5_pass && <span className="text-xs text-accent">Ro5 ✓</span>}
                  </div>
                  {state?.phase === 'error' && state.error && (
                    <p className="mt-1.5 text-xs text-conf-verylow">{state.error}</p>
                  )}
                </div>

                <div className="flex flex-none items-center gap-2 sm:w-40 sm:flex-col sm:items-end">
                  {state && <DockBadge state={state} />}
                  <button
                    type="button"
                    onClick={() => onDock(c.smiles)}
                    disabled={busy}
                    className="w-full max-w-[7rem] rounded-md border border-hairline bg-paper-deep px-3 py-1.5 text-xs font-medium text-ink transition-colors hover:border-[var(--color-accent)] hover:text-accent disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {state?.phase === 'done' || state?.phase === 'error' ? 'Re-dock' : 'Dock'}
                  </button>
                </div>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
