import type { Ranking } from '../../lib/api'

type Props = {
  ranking: Ranking | null
  status: 'idle' | 'loading' | 'done' | 'error'
  error?: string | null
  /** SMILES of the molecule whose poses are currently shown in the viewers. */
  activeSmiles: string | null
  onSelect: (smiles: string) => void
}

function truncateSmiles(smiles: string, max = 22): string {
  return smiles.length > max ? `${smiles.slice(0, max - 1)}…` : smiles
}

/** Signed selectivity, e.g. "+4.10" / "−0.30" (true minus glyph). */
function signedSel(x: number): string {
  const s = x.toFixed(2)
  return x > 0 ? `+${s}` : s.replace('-', '−')
}

/** Selectivity colouring: mutant-selective (positive) reads as accent. */
function selTone(x: number): string {
  if (x > 0.3) return 'text-accent'
  if (x < 0) return 'text-muted'
  return 'text-ink'
}

/**
 * SelectivityBoard — the resistance leaderboard. Unlike the raw-affinity
 * DockedResults, it ranks docked molecules by composite fitness (Stage 7): mutant
 * potency + selectivity margin + drug-likeness, pool-normalised. Clicking a row
 * loads that molecule's WT + mutant poses into the two viewers.
 */
export default function SelectivityBoard({ ranking, status, error, activeSmiles, onSelect }: Props) {
  const rows = ranking?.ranked ?? []
  const best = rows[0]?.scores.selectivity

  return (
    <section className="flex flex-col">
      <div className="mb-4 flex flex-col gap-1 border-t border-hairline pt-6">
        <h2 className="font-display text-base font-medium text-ink">Selectivity ranking</h2>
        <p className="text-xs text-muted">
          {rows.length === 0
            ? 'Dock candidates to rank them by selectivity fitness.'
            : `${rows.length} molecule${rows.length !== 1 ? 's' : ''} · click a row to view its poses.`}
        </p>
      </div>

      {status === 'error' ? (
        <p className="font-mono text-sm text-conf-verylow">{error ?? 'Ranking failed'}</p>
      ) : rows.length === 0 ? (
        <div className="rounded-md border border-dashed border-hairline bg-paper px-4 py-8 text-center">
          <p className="text-sm text-muted">
            {status === 'loading' ? 'Ranking…' : 'No docks yet — dock a candidate to rank it here.'}
          </p>
        </div>
      ) : (
        <div className="overflow-hidden rounded-md border border-hairline bg-paper">
          <ul className="flex flex-col">
            {rows.map((m) => {
              const isActive = m.smiles === activeSmiles
              return (
                <li key={m.smiles}>
                  <div
                    role="button"
                    tabIndex={0}
                    onClick={() => onSelect(m.smiles)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault()
                        onSelect(m.smiles)
                      }
                    }}
                    className={`group flex cursor-pointer items-center gap-3 border-b border-hairline px-3 py-2.5 transition-colors last:border-b-0 ${
                      isActive ? 'border-l-2 border-l-accent bg-accent-soft pl-[10px]' : 'hover:bg-paper-deep'
                    }`}
                  >
                    <span
                      className={`w-5 flex-none text-center font-mono text-sm tabular-nums ${
                        isActive ? 'text-accent' : 'text-muted'
                      }`}
                    >
                      {m.rank}
                    </span>

                    <div className="min-w-0 flex-1">
                      <p className="truncate font-mono text-[11px] text-ink" title={m.smiles}>
                        {truncateSmiles(m.smiles)}
                      </p>
                      <div className="mt-0.5 flex flex-wrap items-center gap-x-2.5 gap-y-0.5 font-mono text-[9px] text-muted">
                        <span className="tabular-nums">wt {m.scores.wt_score.toFixed(1)}</span>
                        <span className="tabular-nums">mut {m.scores.mutant_score.toFixed(1)}</span>
                        {m.scores.qed != null && (
                          <span className="tabular-nums">QED {m.scores.qed.toFixed(2)}</span>
                        )}
                      </div>
                    </div>

                    <div className="flex flex-none flex-col items-end">
                      <span
                        className={`font-mono text-sm tabular-nums ${selTone(m.scores.selectivity)}`}
                      >
                        {signedSel(m.scores.selectivity)}
                      </span>
                      <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-muted">
                        selectivity
                      </span>
                    </div>
                  </div>
                </li>
              )
            })}
          </ul>

          <div className="flex items-center justify-between border-t border-hairline px-3 py-2">
            <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-muted">
              {ranking?.normalization ?? 'zscore'} · w{' '}
              {ranking
                ? `${ranking.weights.selectivity}/${ranking.weights.potency}/${ranking.weights.drug_likeness}`
                : '—'}
            </span>
            {best != null && (
              <span className="font-mono text-[9px] text-muted">
                Best sel <span className={selTone(best)}>{signedSel(best)}</span>
              </span>
            )}
          </div>
        </div>
      )}
    </section>
  )
}
