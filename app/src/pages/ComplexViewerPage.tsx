import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import type { Complex } from '../lib/api'
import { getComplex } from '../lib/api'
import { plddtBands } from '../lib/plddt'
import MolstarViewer from '../components/viewer/MolstarViewer'

const REPRESENTATIONS = [
  { label: 'Spheres', value: 'spacefill' },
  { label: 'Cartoon', value: 'cartoon' },
  { label: 'Surface', value: 'gaussian-surface' },
  { label: 'Ball & stick', value: 'ball-and-stick' },
]

// Spheres by default, per the brief — a fuller view of the fold than cartoon.
const DEFAULT_REPRESENTATION = 'spacefill'

/** A viewer panel that shows the structure, or a "Not available" placeholder. */
function StructurePanel({
  url,
  label,
  plddt,
  representation,
}: {
  url?: string
  label: string
  plddt?: number
  representation: string
}) {
  if (!url) {
    return (
      <div className="flex flex-1 flex-col">
        <div className="flex items-baseline justify-between border-b border-hairline px-3 py-2">
          <span className="font-mono text-[11px] uppercase tracking-[0.12em] text-ink">{label}</span>
        </div>
        <div className="flex min-h-0 flex-1 items-center justify-center bg-paper-deep">
          <span className="font-mono text-xs uppercase tracking-[0.12em] text-muted">
            Not available
          </span>
        </div>
      </div>
    )
  }
  return <MolstarViewer url={url} label={label} plddt={plddt} representation={representation} />
}

/**
 * ComplexViewerPage — a standalone page (route /structure/:id) that renders a
 * protein's AlphaFold monomer and dimer in Mol*, side by side. Structures load
 * directly from their remote .cif URLs; nothing is downloaded to disk.
 */
export default function ComplexViewerPage() {
  const { id = '' } = useParams()
  const [complex, setComplex] = useState<Complex | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [representation, setRepresentation] = useState(DEFAULT_REPRESENTATION)

  useEffect(() => {
    const ctrl = new AbortController()
    setComplex(null)
    setError(null)
    getComplex(id, ctrl.signal)
      .then(setComplex)
      .catch((e) => {
        if (!ctrl.signal.aborted) {
          setError(e instanceof Error ? e.message : 'Failed to load structure')
        }
      })
    return () => ctrl.abort()
  }, [id])

  return (
    <div className="flex min-h-screen flex-col bg-paper">
      {/* Header */}
      <div className="flex flex-none flex-wrap items-center justify-between gap-3 border-b border-hairline px-6 py-3">
        <div className="flex min-w-0 items-center gap-4">
          <Link
            to="/"
            className="font-mono text-[11px] uppercase tracking-[0.1em] text-muted transition-colors hover:text-ink"
          >
            ← Back
          </Link>
          <div className="min-w-0">
            <h1 className="truncate font-display text-lg font-medium text-ink">
              {complex?.gene_name || complex?.uniprot_id || id}
              <span className="ml-2 font-mono text-xs font-normal text-muted">
                {complex?.uniprot_id || id}
              </span>
            </h1>
            {complex?.protein_name && (
              <p className="truncate text-xs text-muted">{complex.protein_name}</p>
            )}
          </div>
        </div>

        <div className="flex flex-wrap rounded-md border border-hairline bg-paper-deep p-0.5">
          {REPRESENTATIONS.map((opt) => (
            <button
              key={opt.value}
              type="button"
              onClick={() => setRepresentation(opt.value)}
              className={`rounded px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.1em] transition-colors ${
                representation === opt.value
                  ? 'bg-paper text-ink shadow-[0_1px_2px_rgba(18,22,28,0.12)]'
                  : 'text-muted hover:text-ink'
              }`}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      {/* Body */}
      {error ? (
        <div className="flex flex-1 items-center justify-center p-6 text-center">
          <p className="font-mono text-sm text-conf-verylow">{error}</p>
        </div>
      ) : !complex ? (
        <div className="flex flex-1 items-center justify-center">
          <span className="animate-pulse font-mono text-xs uppercase tracking-[0.15em] text-muted">
            Loading…
          </span>
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col md:flex-row">
          <div className="flex min-h-[50vh] flex-1 flex-col border-hairline md:min-h-0 md:border-r">
            <StructurePanel
              url={complex.monomer_structure_url}
              label="Monomer · single chain"
              plddt={complex.monomer_plddt_avg}
              representation={representation}
            />
          </div>
          <div className="flex min-h-[50vh] flex-1 flex-col border-t border-hairline md:min-h-0 md:border-t-0">
            <StructurePanel
              url={complex.complex_structure_url}
              label="Dimer · complex"
              plddt={complex.dimer_plddt_avg}
              representation={representation}
            />
          </div>
        </div>
      )}

      {/* pLDDT legend */}
      <div className="flex flex-none flex-wrap items-center justify-center gap-x-5 gap-y-1.5 border-t border-hairline px-6 py-2.5">
        <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted">
          AlphaFold confidence (pLDDT)
        </span>
        {plddtBands.map((band) => (
          <span key={band.label} className="flex items-center gap-1.5">
            <span className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: band.color }} />
            <span className="font-mono text-[10px] text-ink">{band.label}</span>
          </span>
        ))}
      </div>
    </div>
  )
}
