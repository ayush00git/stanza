import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router-dom'
import type { BindingSiteResult, Complex, DockedPose, Pocket } from '../lib/api'
import { getBindingSites, getComplex } from '../lib/api'
import { plddtBands } from '../lib/plddt'
import MolstarViewer, { type HighlightResidue } from '../components/viewer/MolstarViewer'
import BindingSitesPanel, { pocketKey } from '../components/viewer/BindingSitesPanel'

/** Pair each residue index with its chain into Mol* highlight targets. */
function pocketResidues(pocket: Pocket): HighlightResidue[] {
  const indices = pocket.residue_indices ?? []
  const chains = pocket.residue_chains ?? []
  return indices.map((index, i) => ({ chain: chains[i] ?? chains[0] ?? '', index }))
}

const REPRESENTATIONS = [
  { label: 'Spheres', value: 'spacefill' },
  { label: 'Cartoon', value: 'cartoon' },
  { label: 'Surface', value: 'gaussian-surface' },
  { label: 'Ball & stick', value: 'ball-and-stick' },
]

// Spheres by default, per the brief — a fuller view of the fold than cartoon.
const DEFAULT_REPRESENTATION = 'spacefill'

type BsStatus = 'loading' | 'done' | 'error'

/**
 * A single viewer panel: renders the Mol* structure, or a "Not available"
 * placeholder when the structure URL is missing. The docked-ligand pose (raw
 * PDB string) is threaded straight through to the viewer.
 */
function StructurePanel({
  url,
  label,
  plddt,
  representation,
  highlight,
  pose,
}: {
  url?: string
  label: string
  plddt?: number
  representation: string
  highlight?: HighlightResidue[]
  pose?: string | null
}) {
  if (!url) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
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
  return (
    <MolstarViewer
      url={url}
      label={label}
      plddt={plddt}
      representation={representation}
      highlight={highlight}
      pose={pose}
    />
  )
}

/** A tidy label/value cell for the header metadata strip. */
function MetaItem({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="font-mono text-[9px] uppercase tracking-[0.14em] text-muted">{label}</span>
      <span className="text-[13px] text-ink">{children}</span>
    </div>
  )
}

/**
 * Subtle caption shown beneath a viewer while a docked pose is loaded into it.
 * Surfaces which pocket the ligand sits in and its affinity, with a way out.
 */
function PoseCaption({ pose, onClear }: { pose: DockedPose; onClear: () => void }) {
  return (
    <div className="flex items-center justify-between gap-3 border-t border-hairline bg-accent-soft px-3 py-1.5">
      <span className="min-w-0 truncate font-mono text-[10px] uppercase tracking-[0.1em] text-accent">
        Docked pose · P{pose.pocket_id}
        {pose.binding_affinity != null && ` · ${pose.binding_affinity} kcal/mol`}
        {pose.chembl_id && ` · ${pose.chembl_id}`}
      </span>
      <button
        type="button"
        onClick={onClear}
        className="flex-none font-mono text-[10px] uppercase tracking-[0.1em] text-muted transition-colors hover:text-ink"
      >
        Clear
      </button>
    </div>
  )
}

/**
 * ComplexViewerPage — route /structure/:id. Renders the AlphaFold monomer and
 * dimer in Mol* (collapsible, default-open) as soon as the fast getComplex
 * metadata resolves, while the slow fpocket binding-site analysis streams in
 * below. Selecting a pocket highlights its residues in the matching viewer;
 * docking a fragment renders the resulting ligand pose in that same viewer.
 */
export default function ComplexViewerPage() {
  const { id = '' } = useParams()
  const [complex, setComplex] = useState<Complex | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [representation, setRepresentation] = useState(DEFAULT_REPRESENTATION)
  const [structuresOpen, setStructuresOpen] = useState(true)

  // fpocket analysis lifecycle — kept fully separate from the structure load
  // so the viewers never wait on this (slow) request.
  const [bs, setBs] = useState<BindingSiteResult | null>(null)
  const [bsStatus, setBsStatus] = useState<BsStatus>('loading')
  const [bsError, setBsError] = useState<string | null>(null)

  // Selected pocket — kept SEPARATELY per structure so each viewer's highlight
  // persists independently. Clicking a pocket replaces the selection for its own
  // structure only; it never toggles off and never clears the other structure.
  const [selectedMonomer, setSelectedMonomer] = useState<Pocket | null>(null)
  const [selectedDimer, setSelectedDimer] = useState<Pocket | null>(null)

  // Docked-ligand pose — also per structure, so each viewer shows the pose that
  // was docked into it.
  const [monomerPose, setMonomerPose] = useState<DockedPose | null>(null)
  const [dimerPose, setDimerPose] = useState<DockedPose | null>(null)

  const selectedKeys = useMemo(() => {
    const keys = new Set<string>()
    if (selectedMonomer) keys.add(pocketKey(selectedMonomer))
    if (selectedDimer) keys.add(pocketKey(selectedDimer))
    return keys
  }, [selectedMonomer, selectedDimer])

  // Route each click to its own structure's selection so the Mol* highlight
  // persists per viewer.
  const handleSelect = (p: Pocket) => {
    if (p.source_type === 'monomer') setSelectedMonomer(p)
    else setSelectedDimer(p)
  }

  // Route a finished docking pose into the viewer for its structure.
  const handlePose = (pose: DockedPose) => {
    if (pose.source_type === 'monomer') setMonomerPose(pose)
    else setDimerPose(pose)
  }

  // Each structure's selection routes to its own viewer's highlight; an empty
  // array leaves a viewer un-highlighted until one of its pockets is clicked.
  const monomerHighlight = useMemo<HighlightResidue[]>(
    () => (selectedMonomer ? pocketResidues(selectedMonomer) : []),
    [selectedMonomer],
  )
  const dimerHighlight = useMemo<HighlightResidue[]>(
    () => (selectedDimer ? pocketResidues(selectedDimer) : []),
    [selectedDimer],
  )

  // Metadata (fast) and fpocket analysis (slow) load in parallel on mount and
  // are tracked in independent state with independent error handling, so the
  // structure viewers can render the moment getComplex resolves.
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

    setSelectedMonomer(null)
    setSelectedDimer(null)
    setMonomerPose(null)
    setDimerPose(null)
    setBs(null)
    setBsError(null)
    setBsStatus('loading')
    getBindingSites(id, ctrl.signal)
      .then((r) => {
        setBs(r)
        setBsStatus('done')
      })
      .catch((e) => {
        if (!ctrl.signal.aborted) {
          setBsError(e instanceof Error ? e.message : 'Binding-site analysis failed')
          setBsStatus('error')
        }
      })

    return () => ctrl.abort()
  }, [id])

  // Metadata fields for the header strip — only rendered when present.
  const metaItems = useMemo(() => {
    if (!complex) return []
    const items: { label: string; value: ReactNode }[] = []
    if (complex.organism) {
      items.push({ label: 'Organism', value: <span className="italic">{complex.organism}</span> })
    }
    if (complex.drug_count != null && complex.drug_count >= 0) {
      items.push({
        label: 'Known drugs',
        value: complex.drug_count === 0 ? 'Undrugged' : `${complex.drug_count}`,
      })
    }
    if (complex.category) items.push({ label: 'Category', value: complex.category })
    return items
  }, [complex])

  return (
    <div className="flex min-h-screen flex-col bg-paper">
      {/* Header — target identity plus a tidy metadata strip */}
      <header className="sticky top-0 z-10 flex-none border-b border-hairline bg-paper/90 backdrop-blur-sm">
        <div className="mx-auto flex w-full max-w-6xl flex-col gap-3 px-6 py-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex min-w-0 items-center gap-4">
              <Link
                to="/"
                className="font-mono text-[11px] uppercase tracking-[0.1em] text-muted transition-colors hover:text-ink"
              >
                ← Back
              </Link>
              <div className="min-w-0">
                <h1 className="truncate font-display text-xl font-medium text-ink">
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
            {complex?.is_who_pathogen && (
              <span className="flex-none rounded-full border border-accent/40 bg-accent-soft px-2.5 py-1 font-mono text-[9px] uppercase tracking-[0.12em] text-accent">
                WHO pathogen
              </span>
            )}
          </div>

          {metaItems.length > 0 && (
            <div className="flex flex-wrap gap-x-8 gap-y-2 border-t border-hairline pt-2.5">
              {metaItems.map((item) => (
                <MetaItem key={item.label} label={item.label}>
                  {item.value}
                </MetaItem>
              ))}
            </div>
          )}
        </div>
      </header>

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
        <div className="mx-auto flex w-full max-w-6xl flex-col gap-10 px-6 py-8">
          {/* ── Structures ─────────────────────────────────────────────
              Renders purely from `complex`; never gated on the fpocket
              analysis below. Collapsible, but default-open and prominent. */}
          <section className="flex flex-col">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <button
                type="button"
                onClick={() => setStructuresOpen((v) => !v)}
                className="flex items-center gap-2 font-display text-base font-medium text-ink transition-colors hover:text-accent"
                aria-expanded={structuresOpen}
              >
                <span className="font-mono text-xs text-muted">{structuresOpen ? '▾' : '▸'}</span>
                Structures
              </button>

              {structuresOpen && (
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
              )}
            </div>

            {structuresOpen && (
              <div className="mt-4 flex flex-col overflow-hidden rounded-lg border border-hairline bg-paper-deep">
                {/* Comfortable, side-by-side on md+, stacked on small screens.
                    Each viewer manages its own loading state internally. */}
                <div className="flex min-h-[420px] flex-col md:h-[56vh] md:min-h-[460px] md:flex-row">
                  <div className="flex min-h-[360px] flex-1 flex-col border-hairline max-md:border-b md:min-h-0 md:border-r">
                    <StructurePanel
                      url={complex.monomer_structure_url}
                      label="Monomer · single chain"
                      plddt={complex.monomer_plddt_avg}
                      representation={representation}
                      highlight={monomerHighlight}
                      pose={monomerPose?.pdb ?? null}
                    />
                    {monomerPose && (
                      <PoseCaption pose={monomerPose} onClear={() => setMonomerPose(null)} />
                    )}
                  </div>
                  <div className="flex min-h-[360px] flex-1 flex-col md:min-h-0">
                    <StructurePanel
                      url={complex.complex_structure_url}
                      label="Dimer · complex"
                      plddt={complex.dimer_plddt_avg}
                      representation={representation}
                      highlight={dimerHighlight}
                      pose={dimerPose?.pdb ?? null}
                    />
                    {dimerPose && (
                      <PoseCaption pose={dimerPose} onClear={() => setDimerPose(null)} />
                    )}
                  </div>
                </div>

                {/* pLDDT legend */}
                <div className="flex flex-wrap items-center justify-center gap-x-5 gap-y-1.5 border-t border-hairline px-6 py-2.5">
                  <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted">
                    AlphaFold confidence (pLDDT)
                  </span>
                  {plddtBands.map((band) => (
                    <span key={band.label} className="flex items-center gap-1.5">
                      <span
                        className="h-2.5 w-2.5 rounded-full"
                        style={{ backgroundColor: band.color }}
                      />
                      <span className="font-mono text-[10px] text-ink">{band.label}</span>
                    </span>
                  ))}
                </div>
              </div>
            )}
          </section>

          {/* ── Binding-site analysis ──────────────────────────────────
              Streams in on its own; shows its own loading/error state while
              fpocket runs. Fragment docking lives inline in each pocket card
              and reports finished poses back up via onPose. */}
          <section className="flex flex-col">
            <div className="mb-4 flex flex-col gap-1 border-t border-hairline pt-6">
              <h2 className="font-display text-base font-medium text-ink">Binding-site analysis</h2>
              <p className="text-xs text-muted">
                Druggable pockets detected by fpocket. Dock a fragment in any pocket to view its pose
                above.
              </p>
            </div>
            <BindingSitesPanel
              status={bsStatus}
              result={bs}
              error={bsError}
              selectedKeys={selectedKeys}
              onSelect={handleSelect}
              onPose={handlePose}
              uniprotId={complex.uniprot_id}
              structureUrls={{
                monomer: complex.monomer_structure_url,
                dimer: complex.complex_structure_url,
              }}
            />
          </section>
        </div>
      )}
    </div>
  )
}
