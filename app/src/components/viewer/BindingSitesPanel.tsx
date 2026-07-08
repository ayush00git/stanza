import type { BindingSiteResult, DockedPose, Pocket } from '../../lib/api'
import { plddtBand } from '../../lib/plddt'
import DockingPanel from './DockingPanel'

type Status = 'loading' | 'done' | 'error'

/** Stable identity for a pocket (pocket_id repeats across monomer/dimer). */
export function pocketKey(p: Pocket): string {
  return `${p.source_type}-${p.pocket_id}`
}

function Chip({ label, value }: { label: string; value: string | number }) {
  return (
    <span className="inline-flex items-baseline gap-1.5 rounded-md border border-hairline bg-paper px-2.5 py-1">
      <span className="font-mono text-sm text-ink tabular-nums">{value}</span>
      <span className="font-mono text-[10px] uppercase tracking-[0.1em] text-muted">
        {label}
      </span>
    </span>
  )
}

/** A single labelled metric: caption above, value below. */
function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-muted">
        {label}
      </span>
      <span className="font-mono text-xs text-ink tabular-nums">{value}</span>
    </div>
  )
}

/**
 * One detected pocket, with every fpocket metric clearly labelled. `rank` is
 * the pocket's position within its (druggability-sorted) column; rank 0 is the
 * most druggable and gets a "Top" marker.
 */
function PocketRow({
  pocket,
  rank,
  active,
  onSelect,
  uniprotId,
  onPose,
  structureUrls,
}: {
  pocket: Pocket
  rank: number
  active: boolean
  onSelect: (p: Pocket) => void
  uniprotId?: string
  // onPose lifts a completed dock up to the page for 3D display.
  onPose?: (pose: DockedPose) => void
  // Receptor structure URLs, keyed by source type; the pocket docks against
  // the same structure fpocket detected it in, so its center coords line up.
  structureUrls?: { monomer?: string; dimer?: string }
}) {
  const score = Math.max(0, Math.min(1, pocket.druggability_score))
  const residues = pocket.residue_indices?.length ?? 0
  const isTop = rank === 0
  // Dock against the structure this pocket came from (monomer vs dimer).
  const proteinPdbPath =
    pocket.source_type === 'monomer' ? structureUrls?.monomer : structureUrls?.dimer
  return (
    <li
      onClick={() => onSelect(pocket)}
      aria-selected={active}
      className={`grid cursor-pointer grid-cols-[2.5rem_1fr] gap-x-3 border-b border-hairline px-2 py-4 transition-colors last:border-b-0 ${
        active ? 'bg-accent-soft' : 'hover:bg-paper-deep'
      }`}
    >
      <span className={`font-mono text-xs ${active ? 'text-accent' : 'text-muted'}`}>
        P{pocket.pocket_id}
      </span>

      <div>
        {/* Druggability — the headline metric, labelled and ranked */}
        <div className="flex items-baseline justify-between">
          <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-muted">
            Druggability
          </span>
          <span className="flex items-center gap-2">
            {isTop && (
              <span className="rounded-full bg-accent px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.1em] text-paper">
                Top
              </span>
            )}
            <span className="font-mono text-sm text-ink tabular-nums">
              {pocket.druggability_score.toFixed(2)}
            </span>
          </span>
        </div>
        <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-paper-deep">
          <div className="h-full rounded-full bg-accent" style={{ width: `${score * 100}%` }} />
        </div>

        {/* Flags */}
        {(pocket.is_interface_pocket || pocket.is_emergent || pocket.is_conserved) && (
          <div className="mt-2.5 flex flex-wrap gap-1.5">
            {pocket.is_interface_pocket && (
              <span className="rounded-full bg-accent-soft px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.1em] text-accent">
                Interface
              </span>
            )}
            {pocket.is_emergent && (
              <span className="rounded-full bg-conf-verylow/15 px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.1em] text-ink">
                Emergent
              </span>
            )}
            {pocket.is_conserved && (
              <span className="rounded-full border border-hairline px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.1em] text-muted">
                Conserved
              </span>
            )}
          </div>
        )}

        {/* Every fpocket metric, labelled */}
        <div className="mt-3 grid grid-cols-3 gap-x-4 gap-y-2.5">
          <Stat label="Volume Å³" value={pocket.volume.toFixed(0)} />
          <Stat label="Surface Å²" value={pocket.surface_area.toFixed(0)} />
          <Stat label="Depth Å" value={pocket.depth.toFixed(1)} />
          <Stat label="Hydrophobicity" value={pocket.hydrophobicity.toFixed(1)} />
          <Stat label="Polarity" value={pocket.polarity.toFixed(1)} />
          <div className="flex flex-col gap-0.5">
            <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-muted">
              Avg pLDDT
            </span>
            <span className="inline-flex items-center gap-1.5 font-mono text-xs text-ink tabular-nums">
              {pocket.avg_plddt > 0 ? (
                <>
                  <span
                    className="h-2 w-2 rounded-full"
                    style={{ backgroundColor: plddtBand(pocket.avg_plddt).color }}
                  />
                  {pocket.avg_plddt.toFixed(1)}
                </>
              ) : (
                '—'
              )}
            </span>
          </div>
          <Stat label="Residues" value={String(residues)} />
          {pocket.chains && pocket.chains.length > 0 && (
            <Stat label="Chains" value={pocket.chains.join(' / ')} />
          )}
        </div>

        {/* Fragment docking is rendered inline for the selected pocket. Stop
            click bubbling so button/input interactions don't re-toggle the row. */}
        {active && (
          <div
            className="mt-4 border-t border-hairline pt-4"
            onClick={(e) => e.stopPropagation()}
          >
            <DockingPanel
              pocket={pocket}
              uniprotId={uniprotId}
              proteinPdbPath={proteinPdbPath}
              onPose={onPose}
              compact
            />
          </div>
        )}
      </div>
    </li>
  )
}

/** Sort a copy of the pockets by druggability, most druggable first. */
function byDruggability(pockets: Pocket[]): Pocket[] {
  return [...pockets].sort((a, b) => b.druggability_score - a.druggability_score)
}

function PocketColumn({
  title,
  pockets,
  selectedKeys,
  onSelect,
  uniprotId,
  onPose,
  structureUrls,
}: {
  title: string
  pockets: Pocket[]
  selectedKeys: Set<string>
  onSelect: (p: Pocket) => void
  uniprotId?: string
  onPose?: (pose: DockedPose) => void
  structureUrls?: { monomer?: string; dimer?: string }
}) {
  const sorted = byDruggability(pockets)
  return (
    <div className="min-w-0 flex-1">
      <div className="flex items-baseline justify-between border-b border-hairline pb-2">
        <span className="font-mono text-[11px] uppercase tracking-[0.12em] text-ink">
          {title}
        </span>
        <span className="font-mono text-[11px] text-muted">
          {sorted.length} · by druggability
        </span>
      </div>
      {sorted.length === 0 ? (
        <p className="py-4 font-mono text-xs text-muted">No pockets.</p>
      ) : (
        <ul>
          {sorted.map((p, i) => (
            <PocketRow
              key={pocketKey(p)}
              pocket={p}
              rank={i}
              active={selectedKeys.has(pocketKey(p))}
              onSelect={onSelect}
              uniprotId={uniprotId}
              onPose={onPose}
              structureUrls={structureUrls}
            />
          ))}
        </ul>
      )}
    </div>
  )
}

/**
 * BindingSitesPanel — renders fpocket analysis results for the structure page:
 * a summary, then dimer and monomer pockets side by side. It owns only
 * presentation; the fetch/lifecycle lives in ComplexViewerPage.
 */
export default function BindingSitesPanel({
  status,
  result,
  error,
  selectedKeys,
  onSelect,
  uniprotId,
  onPose,
  structureUrls,
}: {
  status: Status
  result: BindingSiteResult | null
  error: string | null
  selectedKeys: Set<string>
  onSelect: (p: Pocket) => void
  uniprotId?: string
  // onPose lifts a completed dock up to the page for 3D display.
  onPose?: (pose: DockedPose) => void
  // Receptor structure URLs so each pocket docks against its own structure.
  structureUrls?: { monomer?: string; dimer?: string }
}) {
  return (
    <section className="w-full">
      <div className="flex items-baseline justify-between">
        <h2 className="font-display text-xl font-medium text-ink">Binding sites</h2>
        <span className="font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
          fpocket analysis
        </span>
      </div>

      {status === 'loading' && (
        <p className="mt-6 animate-pulse font-mono text-xs uppercase tracking-[0.15em] text-muted">
          Running fpocket on monomer and dimer…
        </p>
      )}

      {status === 'error' && (
        <p className="mt-6 font-mono text-sm text-conf-verylow">{error}</p>
      )}

      {status === 'done' && result && (
        <>
          <div className="mt-5 flex flex-wrap gap-2">
            <Chip label="Dimer pockets" value={result.total_pockets} />
            <Chip label="Interface" value={result.interface_pocket_count} />
            <Chip label="Monomer pockets" value={result.monomer_total_pockets} />
            {result.comparison && (
              <>
                <Chip
                  label="Emergent"
                  value={result.comparison.pocket_mapping.emergent_count}
                />
                <Chip label="ΔDGI" value={result.comparison.ddgi.toFixed(2)} />
              </>
            )}
          </div>

          {result.total_pockets === 0 && result.monomer_total_pockets === 0 ? (
            <p className="mt-6 text-sm text-muted">
              fpocket found no pockets on either structure.
            </p>
          ) : (
            <div className="mt-6 flex flex-col gap-8 sm:flex-row sm:gap-10">
              <PocketColumn
                title="Monomer"
                pockets={result.monomer_pockets}
                selectedKeys={selectedKeys}
                onSelect={onSelect}
                uniprotId={uniprotId}
                onPose={onPose}
                structureUrls={structureUrls}
              />
              <PocketColumn
                title="Dimer · complex"
                pockets={result.pockets}
                selectedKeys={selectedKeys}
                onSelect={onSelect}
                uniprotId={uniprotId}
                onPose={onPose}
                structureUrls={structureUrls}
              />
            </div>
          )}
        </>
      )}
    </section>
  )
}
