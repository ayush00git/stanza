import { useState } from 'react'
import type { Complex } from '../lib/api'
import { getComplex } from '../lib/api'
import { plddtBand } from '../lib/plddt'

const categoryLabels: Record<string, string> = {
  who_pathogen: 'WHO priority pathogen',
  human_disease: 'Human disease',
  high_disorder_delta: 'High disorder Δ',
  monomer_only: 'Monomer only',
}

function PlddtChip({ label, value }: { label: string; value: number }) {
  const has = value > 0
  return (
    <span className="inline-flex items-center gap-1.5 rounded-md border border-hairline px-2 py-1 font-mono text-[11px] text-ink">
      <span
        className="h-2 w-2 rounded-full"
        style={{ backgroundColor: has ? plddtBand(value).color : 'var(--color-hairline)' }}
      />
      {label} {has ? value.toFixed(1) : '—'}
    </span>
  )
}

export default function ComplexCard({ complex }: { complex: Complex }) {
  const [detail, setDetail] = useState<Complex | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Search results carry drug_count = -1 (not fetched). Detail fills it in.
  const drugs = detail ?? complex
  const drugKnown = drugs.drug_count >= 0
  const diseases = complex.disease_associations ?? []

  async function loadDetail() {
    if (loading) return
    setLoading(true)
    setError(null)
    try {
      setDetail(await getComplex(complex.uniprot_id))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load detail')
    } finally {
      setLoading(false)
    }
  }

  return (
    <article className="flex flex-col rounded-xl border border-hairline bg-paper p-6 transition-shadow hover:shadow-[0_18px_40px_-30px_rgba(18,22,28,0.5)]">
      <header className="flex items-baseline justify-between gap-3">
        <h3 className="font-display text-xl font-medium text-ink">
          {complex.gene_name || complex.uniprot_id}
        </h3>
        <a
          href={`https://www.uniprot.org/uniprotkb/${complex.uniprot_id}`}
          target="_blank"
          rel="noreferrer"
          className="font-mono text-xs text-accent underline decoration-transparent underline-offset-2 transition-colors hover:decoration-accent"
        >
          {complex.uniprot_id}
        </a>
      </header>

      <p className="mt-1 text-sm leading-snug text-ink">{complex.protein_name}</p>
      <p className="mt-0.5 font-display text-sm italic text-muted">
        {complex.organism}
      </p>

      <div className="mt-4 flex flex-wrap gap-1.5">
        {complex.category && (
          <span className="rounded-full bg-accent-soft px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.1em] text-accent">
            {categoryLabels[complex.category] ?? complex.category}
          </span>
        )}
        {complex.is_who_pathogen && (
          <span className="rounded-full bg-conf-verylow/15 px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.1em] text-ink">
            WHO pathogen
          </span>
        )}
        {complex.review_status === 'reviewed' && (
          <span className="rounded-full border border-hairline px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.1em] text-muted">
            Swiss-Prot
          </span>
        )}
      </div>

      <div className="mt-4 flex flex-wrap gap-2">
        <PlddtChip label="Monomer" value={complex.monomer_plddt_avg} />
        <PlddtChip label="Dimer" value={complex.dimer_plddt_avg} />
        {complex.disorder_delta > 0 && (
          <span
            className="inline-flex items-center gap-1.5 rounded-md bg-accent-soft px-2 py-1 font-mono text-[11px] text-accent"
            title="Confidence gained as a dimer — regions that order on binding"
          >
            Orders on binding +{complex.disorder_delta.toFixed(1)}
          </span>
        )}
      </div>

      {diseases.length > 0 && (
        <p className="mt-4 text-xs leading-relaxed text-muted">
          <span className="font-mono uppercase tracking-[0.1em]">Disease</span>{' '}
          {diseases.slice(0, 3).join(', ')}
          {diseases.length > 3 && ` +${diseases.length - 3}`}
        </p>
      )}

      <div className="mt-5 flex flex-1 items-end justify-between border-t border-hairline pt-4">
        <div className="text-xs text-muted">
          {drugKnown ? (
            drugs.drug_count > 0 ? (
              <span className="text-ink">{drugs.drug_count} known drugs</span>
            ) : (
              <span>No known drugs</span>
            )
          ) : (
            <button
              type="button"
              onClick={loadDetail}
              disabled={loading}
              className="font-medium text-accent underline decoration-hairline underline-offset-2 transition-colors hover:decoration-accent disabled:opacity-50"
            >
              {loading ? 'Loading…' : 'Load drug coverage'}
            </button>
          )}
        </div>

        {complex.complex_structure_url || complex.monomer_structure_url ? (
          <a
            href={complex.complex_structure_url || complex.monomer_structure_url}
            target="_blank"
            rel="noreferrer"
            className="font-mono text-xs text-ink underline decoration-hairline underline-offset-4 transition-colors hover:decoration-accent"
          >
            Structure ↗
          </a>
        ) : null}
      </div>

      {error && <p className="mt-2 text-xs text-conf-verylow">{error}</p>}

      {drugKnown && (drugs.known_drug_names?.length ?? 0) > 0 && (
        <ul className="mt-3 flex flex-wrap gap-1.5">
          {drugs.known_drug_names!.slice(0, 6).map((name) => (
            <li
              key={name}
              className="rounded-md bg-paper-deep px-2 py-0.5 font-mono text-[11px] text-muted"
            >
              {name}
            </li>
          ))}
        </ul>
      )}
    </article>
  )
}
