import type { DockedPose } from '../../lib/api'

const CHEMBL_BASE_URL = 'https://www.ebi.ac.uk/chembl/compound_report_card/'

/**
 * Stable identity for a docked result — one entry per fragment docked into a
 * given pocket on a given structure. Re-docking the same pair replaces the row
 * rather than appending a duplicate. Exported so the page can reuse it when it
 * upserts results and tracks the active selection.
 */
export function entryKey(e: DockedPose): string {
  return `${e.source_type}-${e.pocket_id}-${e.chembl_id ?? ''}`
}

type Props = {
  entries: DockedPose[]
  /** entryKey of the result currently rendered in the 3D viewer, if any. */
  activeKey: string | null
  onSelect: (entry: DockedPose) => void
  onRemove: (entry: DockedPose) => void
  onClear: () => void
}

/** Affinity colouring: stronger (more negative) reads as accent, weaker stays quiet. */
function affinityTone(affinity: number | undefined): string {
  if (affinity == null) return 'text-muted'
  if (affinity <= -7) return 'text-accent'
  if (affinity <= -5) return 'text-ink'
  return 'text-muted'
}

/**
 * DockedResults — a compact "recent docks" leaderboard. Lists every completed
 * docking pose lifted up from the pocket panels, sorted strongest-first (most
 * negative binding affinity ranks #1). Clicking a row loads that pose into the
 * matching 3D viewer; the active row is marked with the accent.
 */
export default function DockedResults({
  entries,
  activeKey,
  onSelect,
  onRemove,
  onClear,
}: Props) {
  const sorted = [...entries].sort(
    (a, b) => (a.binding_affinity ?? 0) - (b.binding_affinity ?? 0),
  )
  const best = sorted[0]?.binding_affinity

  return (
    <section className="flex flex-col">
      <div className="mb-4 flex flex-wrap items-baseline justify-between gap-2 border-t border-hairline pt-6">
        <div className="flex flex-col gap-1">
          <h2 className="font-display text-base font-medium text-ink">Docking results</h2>
          <p className="text-xs text-muted">
            {entries.length === 0
              ? 'Poses you dock appear here, ranked by binding affinity.'
              : `${entries.length} dock${entries.length !== 1 ? 's' : ''} · click a row to view its pose in 3D.`}
          </p>
        </div>
        {entries.length > 0 && (
          <button
            type="button"
            onClick={onClear}
            className="font-mono text-[10px] uppercase tracking-[0.1em] text-muted transition-colors hover:text-ink"
          >
            Clear all
          </button>
        )}
      </div>

      {entries.length === 0 ? (
        <div className="rounded-md border border-dashed border-hairline bg-paper px-4 py-8 text-center">
          <p className="text-sm text-muted">
            No docks yet — dock a fragment to a pocket to see results here.
          </p>
        </div>
      ) : (
        <div className="overflow-hidden rounded-md border border-hairline bg-paper">
          <ul className="flex flex-col">
            {sorted.map((entry, idx) => {
              const rank = idx + 1
              const key = entryKey(entry)
              const isActive = key === activeKey
              return (
                <li key={key}>
                  <div
                    role="button"
                    tabIndex={0}
                    onClick={() => onSelect(entry)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault()
                        onSelect(entry)
                      }
                    }}
                    className={`group flex cursor-pointer items-center gap-3 border-b border-hairline px-4 py-2.5 transition-colors last:border-b-0 ${
                      isActive
                        ? 'border-l-2 border-l-accent bg-accent-soft pl-[14px]'
                        : 'hover:bg-paper-deep'
                    }`}
                  >
                    <span
                      className={`w-5 flex-none text-center font-mono text-sm tabular-nums ${
                        isActive ? 'text-accent' : 'text-muted'
                      }`}
                    >
                      {rank}
                    </span>

                    <div className="min-w-0 flex-1">
                      <div className="flex items-baseline gap-2">
                        <span className="truncate text-sm text-ink">
                          {entry.name || entry.chembl_id || 'Fragment'}
                        </span>
                      </div>
                      <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5">
                        <span className="font-mono text-[10px] uppercase tracking-[0.1em] text-muted">
                          {entry.source_type} · Pocket P{entry.pocket_id}
                        </span>
                        {entry.chembl_id && (
                          <a
                            href={`${CHEMBL_BASE_URL}${entry.chembl_id}`}
                            target="_blank"
                            rel="noopener noreferrer"
                            onClick={(e) => e.stopPropagation()}
                            title={`View ${entry.chembl_id} on ChEMBL`}
                            className="font-mono text-[10px] text-accent transition-colors hover:underline"
                          >
                            {entry.chembl_id} ↗
                          </a>
                        )}
                      </div>
                    </div>

                    <div className="flex flex-none flex-col items-end">
                      <span
                        className={`font-mono text-sm tabular-nums ${affinityTone(entry.binding_affinity)}`}
                      >
                        {entry.binding_affinity != null
                          ? entry.binding_affinity.toFixed(1)
                          : '—'}
                      </span>
                      <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-muted">
                        kcal/mol
                      </span>
                    </div>

                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation()
                        onRemove(entry)
                      }}
                      title="Remove"
                      className="flex h-5 w-5 flex-none items-center justify-center rounded text-muted opacity-0 transition-all hover:bg-conf-verylow/15 hover:text-ink group-hover:opacity-100"
                    >
                      ×
                    </button>
                  </div>
                </li>
              )
            })}
          </ul>

          <div className="flex items-center justify-between border-t border-hairline px-4 py-2">
            <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-muted">
              {entries.length} result{entries.length !== 1 ? 's' : ''}
            </span>
            {best != null && (
              <span className="font-mono text-[9px] text-muted">
                Best <span className="text-accent">{best.toFixed(1)}</span> kcal/mol
              </span>
            )}
          </div>
        </div>
      )}
    </section>
  )
}
