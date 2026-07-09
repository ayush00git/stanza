import { useState } from 'react'
import type { Candidate } from '../../lib/api'
import Thinking, { GEN_PHASES } from '../Thinking'

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
  onGenerate: (n: number) => void
  /** Docking state keyed by candidate SMILES. */
  dockState: Record<string, CandidateDockState>
  onDock: (smiles: string) => void
  /** False while the run isn't ready to generate (e.g. no mutant structure). */
  canGenerate: boolean
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
  onGenerate,
  dockState,
  onDock,
  canGenerate,
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

      {generateError && <p className="mt-3 text-sm text-conf-verylow">{generateError}</p>}

      {/* Regenerating over an existing list: the list stays put, so the waiting
          indicator has to live up here or it would not be seen at all. */}
      {generating && candidates.length > 0 && (
        <Thinking phases={GEN_PHASES} intervalMs={3200} className="mt-3" />
      )}

      {candidates.length === 0 ? (
        <div className="mt-4 rounded-lg border border-dashed border-hairline bg-paper-deep/40 px-6 py-12 text-center">
          {generating ? (
            <Thinking phases={GEN_PHASES} intervalMs={3200} className="justify-center" />
          ) : (
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
