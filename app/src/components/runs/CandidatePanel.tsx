import { useState } from 'react'
import {
  dropReasonLabel,
  type Candidate,
  type GenerateProgress,
  type MoleculeCheck,
  type ValidationSummary,
} from '../../lib/api'

/** Per-candidate docking phase, keyed by SMILES in the page's dockState map. */
export type CandidatePhase = 'docking' | 'done' | 'error'
export type CandidateDockState = {
  phase: CandidatePhase
  selectivity?: number
  error?: string
}

type Props = {
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
  onDock: (smiles: string) => void
  /** False while the run isn't ready to generate (e.g. no mutant structure). */
  canGenerate: boolean
  /** How many molecules have been docked — these are what regeneration feeds back. */
  dockedCount: number
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

  return (
    <div className="mt-3 rounded-md border border-hairline bg-paper-deep/40 px-3 py-2.5" aria-live="polite">
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="inline-flex items-center gap-1.5 text-xs font-medium text-accent">
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-accent" />
          {progress?.stage === 'claude' ? 'Claude is designing' : 'Generating'}
        </span>
        <span className="text-xs text-muted">
          {progress?.message ?? 'starting'}
          {progress && progress.total > 0 && ` · step ${progress.done} of ${progress.total}`}
        </span>
      </div>

      <div className="mt-2 h-0.5 overflow-hidden rounded-full bg-hairline">
        <div
          className="h-full rounded-full bg-accent transition-[width] duration-500"
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
  dockedCount,
  covalentResidue,
}: Props) {
  const [n, setN] = useState(6)

  return (
    <div>
      {/* Generate control. */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-sm font-medium text-ink">Candidate molecules</span>
        <div className="flex items-center gap-2">
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
            {generating ? 'Generating…' : 'Generate'}
          </button>
        </div>
      </div>

      {/* Once molecules have been docked, regeneration is a feedback round: every docked
          result (ranked by covalent feasibility) travels back into the design prompt, so
          Claude sees which warheads actually reached the thiol and which missed. This is
          the headline capability, so it is highlighted rather than muted. It states the
          MECHANISM — what is fed back — not an outcome: whether the next batch scores
          higher is a measured question, and the leaderboard is where it is read. */}
      {!generating && dockedCount > 0 && (
        <div className="mt-3 flex items-start gap-2.5 rounded-md border border-accent/40 bg-accent-soft px-3.5 py-2.5">
          <span className="mt-1.5 h-1.5 w-1.5 flex-none rounded-full bg-accent" />
          <p className="text-xs leading-relaxed text-ink">
            <span className="font-medium text-accent">Feedback round.</span> Every molecule
            you've docked is fed back to Claude, ranked by which warhead best reached{' '}
            {covalentResidue ?? 'the target residue'} — so the next batch is designed against
            measured geometry, not a blank pocket. Regenerate to close the loop.
          </p>
        </div>
      )}

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
