import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import type { Complex } from '../lib/api'
import { getComplex } from '../lib/api'
import { plddtBand } from '../lib/plddt'

// Category → human label. `monomer_only` is intentionally omitted: it carries no
// information beyond "no dimer", which the structure line already states.
const categoryLabels: Record<string, string> = {
  who_pathogen: 'WHO priority pathogen',
  human_disease: 'Human disease',
  high_disorder_delta: 'High disorder Δ',
}

function PlddtChip({ label, value }: { label: string; value: number }) {
  const band = plddtBand(value)
  return (
    <span className="inline-flex items-center gap-1.5 rounded-md border border-hairline px-2 py-1 font-mono text-[11px] text-ink">
      <span className="h-2 w-2 rounded-full" style={{ backgroundColor: band.color }} />
      {label} {value.toFixed(1)}
      <span className="text-muted">{band.label}</span>
    </span>
  )
}

export default function ComplexCard({ complex }: { complex: Complex }) {
  const navigate = useNavigate()
  const [detail, setDetail] = useState<Complex | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // A real dimer exists only when the backend gave us a complex structure URL.
  // Without it, dimer_plddt_avg just mirrors the monomer and must not be shown.
  const hasDimer = Boolean(complex.complex_structure_url)
  const hasMonomer = Boolean(complex.monomer_structure_url)

  // Drug coverage is fetched on demand (search results carry -1). `detail`
  // being set means we tried; a still-negative count means ChEMBL had nothing
  // or was unreachable — either way, no button to click again.
  const drugs = detail ?? complex
  const drugCountKnown = drugs.drug_count >= 0
  const drugAttempted = detail !== null
  const diseases = complex.disease_associations ?? []
  const isWho = complex.category === 'who_pathogen' || complex.is_who_pathogen

  const openViewer = () => navigate(`/structure/${encodeURIComponent(complex.uniprot_id)}`)

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
    <article
      role="button"
      tabIndex={0}
      onClick={openViewer}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          openViewer()
        }
      }}
      className="flex cursor-pointer flex-col rounded-xl border border-hairline bg-paper p-6 transition-shadow hover:shadow-[0_18px_40px_-30px_rgba(18,22,28,0.5)]"
    >
      {/* Identity */}
      <header className="flex items-baseline justify-between gap-3">
        <h3 className="truncate font-display text-xl font-medium text-ink">
          {complex.gene_name || complex.uniprot_id}
        </h3>
        <a
          href={`https://www.uniprot.org/uniprotkb/${complex.uniprot_id}`}
          target="_blank"
          rel="noreferrer"
          onClick={(e) => e.stopPropagation()}
          className="shrink-0 font-mono text-xs text-accent underline decoration-transparent underline-offset-2 transition-colors hover:decoration-accent"
        >
          {complex.uniprot_id}
        </a>
      </header>

      <p className="mt-1 text-sm leading-snug text-ink">{complex.protein_name}</p>
      <p className="mt-0.5 font-display text-sm italic text-muted">{complex.organism}</p>

      {/* Classification badges — no duplicates: WHO is conveyed once. */}
      <div className="mt-4 flex flex-wrap gap-1.5">
        {isWho ? (
          <span className="rounded-full bg-conf-verylow/15 px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.1em] text-ink">
            WHO priority pathogen
          </span>
        ) : (
          complex.category &&
          categoryLabels[complex.category] && (
            <span className="rounded-full bg-accent-soft px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.1em] text-accent">
              {categoryLabels[complex.category]}
            </span>
          )
        )}
        {complex.review_status === 'reviewed' && (
          <span className="rounded-full border border-hairline px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.1em] text-muted">
            Swiss-Prot
          </span>
        )}
      </div>

      {/* Confidence — only real structures get a chip. */}
      <div className="mt-4 flex flex-wrap gap-2">
        {hasMonomer && <PlddtChip label="Monomer" value={complex.monomer_plddt_avg} />}
        {hasDimer && <PlddtChip label="Dimer" value={complex.dimer_plddt_avg} />}
        {hasDimer && complex.disorder_delta > 0 && (
          <span
            className="inline-flex items-center gap-1.5 rounded-md bg-accent-soft px-2 py-1 font-mono text-[11px] text-accent"
            title="Confidence gained as a dimer — regions that order on binding"
          >
            Orders on binding +{complex.disorder_delta.toFixed(1)}
          </span>
        )}
      </div>

      {/* What the 3D view will contain */}
      <p className="mt-3 font-mono text-[11px] text-muted">
        {hasDimer ? 'Monomer + dimer structures' : hasMonomer ? 'Monomer structure only' : 'No structure available'}
      </p>

      {diseases.length > 0 && (
        <p className="mt-4 text-xs leading-relaxed text-muted">
          <span className="font-mono uppercase tracking-[0.1em]">Disease</span>{' '}
          {diseases.slice(0, 3).join(', ')}
          {diseases.length > 3 && ` +${diseases.length - 3}`}
        </p>
      )}

      {/* Footer: drug coverage + open action */}
      <div className="mt-5 flex flex-1 items-end justify-between gap-3 border-t border-hairline pt-4">
        <div className="text-xs text-muted">
          {drugCountKnown ? (
            drugs.drug_count > 0 ? (
              <span className="text-ink">{drugs.drug_count} approved drug{drugs.drug_count === 1 ? '' : 's'}</span>
            ) : (
              <span>No approved drugs</span>
            )
          ) : drugAttempted ? (
            <span>Drug data unavailable</span>
          ) : (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation()
                loadDetail()
              }}
              disabled={loading}
              className="font-medium text-accent underline decoration-hairline underline-offset-2 transition-colors hover:decoration-accent disabled:opacity-50"
            >
              {loading ? 'Loading…' : 'Load drug coverage'}
            </button>
          )}
        </div>

        {hasMonomer || hasDimer ? (
          <span
            aria-hidden
            className="shrink-0 font-mono text-xs text-ink underline decoration-hairline underline-offset-4"
          >
            View 3D →
          </span>
        ) : null}
      </div>

      {error && <p className="mt-2 text-xs text-conf-verylow">{error}</p>}

      {drugCountKnown && (drugs.known_drug_names?.length ?? 0) > 0 && (
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
