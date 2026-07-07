import type { BindingSiteResult, Pocket } from '../../lib/api'
import { plddtBand } from '../../lib/plddt'

type Status = 'loading' | 'done' | 'error'

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

/** One detected pocket: druggability, confidence, and structural flags. */
function PocketRow({ pocket }: { pocket: Pocket }) {
  const score = Math.max(0, Math.min(1, pocket.druggability_score))
  const residues = pocket.residue_indices?.length ?? 0
  return (
    <li className="grid grid-cols-[2.5rem_1fr] gap-x-3 border-b border-hairline py-3 last:border-b-0">
      <span className="font-mono text-xs text-muted">P{pocket.pocket_id}</span>

      <div>
        {/* Druggability score bar */}
        <div className="flex items-center gap-2">
          <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-paper-deep">
            <div
              className="h-full rounded-full bg-accent"
              style={{ width: `${score * 100}%` }}
            />
          </div>
          <span className="font-mono text-xs text-ink tabular-nums">
            {pocket.druggability_score.toFixed(2)}
          </span>
        </div>

        {/* Flags */}
        <div className="mt-2 flex flex-wrap gap-1.5">
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

        {/* Metrics */}
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-[11px] text-muted">
          <span>{pocket.volume.toFixed(0)} Å³</span>
          {pocket.avg_plddt > 0 && (
            <span className="inline-flex items-center gap-1.5">
              <span
                className="h-2 w-2 rounded-full"
                style={{ backgroundColor: plddtBand(pocket.avg_plddt).color }}
              />
              pLDDT {pocket.avg_plddt.toFixed(1)}
            </span>
          )}
          <span>{residues} residues</span>
          {pocket.chains && pocket.chains.length > 0 && (
            <span>chains {pocket.chains.join('/')}</span>
          )}
        </div>
      </div>
    </li>
  )
}

function PocketColumn({ title, pockets }: { title: string; pockets: Pocket[] }) {
  return (
    <div className="min-w-0 flex-1">
      <div className="flex items-baseline justify-between border-b border-hairline pb-2">
        <span className="font-mono text-[11px] uppercase tracking-[0.12em] text-ink">
          {title}
        </span>
        <span className="font-mono text-[11px] text-muted">{pockets.length}</span>
      </div>
      {pockets.length === 0 ? (
        <p className="py-4 font-mono text-xs text-muted">No pockets.</p>
      ) : (
        <ul>
          {pockets.map((p) => (
            <PocketRow key={`${p.source_type}-${p.pocket_id}`} pocket={p} />
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
}: {
  status: Status
  result: BindingSiteResult | null
  error: string | null
}) {
  return (
    <section className="mx-auto w-full max-w-5xl px-6 py-8">
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
              <PocketColumn title="Dimer · complex" pockets={result.pockets} />
              <PocketColumn title="Monomer" pockets={result.monomer_pockets} />
            </div>
          )}
        </>
      )}
    </section>
  )
}
