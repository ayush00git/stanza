import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  dockRunLigand,
  generateCandidates,
  getRun,
  getRunPockets,
  getRunRanking,
  runStructureUrl,
  type Candidate,
  type LigandDock,
  type Ranking,
  type Run,
  type RunPocketAnalysis,
} from '../lib/api'
import MolstarViewer, { type HighlightResidue } from '../components/viewer/MolstarViewer'
import MutationDeltaPanel from '../components/runs/MutationDeltaPanel'
import CandidatePanel, { type CandidateDockState } from '../components/runs/CandidatePanel'
import SelectivityBoard from '../components/runs/SelectivityBoard'

const REPRESENTATIONS = [
  { label: 'Cartoon', value: 'cartoon' },
  { label: 'Surface', value: 'gaussian-surface' },
  { label: 'Ball & stick', value: 'ball-and-stick' },
  { label: 'Spheres', value: 'spacefill' },
]
const DEFAULT_REPRESENTATION = 'cartoon'

type LoadStatus = 'loading' | 'done' | 'error'

/** A tidy label/value cell for the header metadata strip. */
function MetaItem({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="font-mono text-[9px] uppercase tracking-[0.14em] text-muted">{label}</span>
      <span className="text-[13px] text-ink">{children}</span>
    </div>
  )
}

/** One viewer with a "not available" fallback when the structure URL is absent. */
function StructurePanel({
  url,
  label,
  representation,
  highlight,
  pose,
}: {
  url?: string
  label: string
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
          <span className="font-mono text-xs uppercase tracking-[0.12em] text-muted">Not available</span>
        </div>
      </div>
    )
  }
  return (
    <MolstarViewer url={url} label={label} representation={representation} highlight={highlight} pose={pose} />
  )
}

/** Caption shown under a viewer while a docked pose is loaded into it. */
function PoseCaption({ smiles, selectivity, onClear }: { smiles: string; selectivity: number; onClear: () => void }) {
  const sel = selectivity > 0 ? `+${selectivity.toFixed(2)}` : selectivity.toFixed(2).replace('-', '−')
  return (
    <div className="flex items-center justify-between gap-3 border-t border-hairline bg-accent-soft px-3 py-1.5">
      <span className="min-w-0 truncate font-mono text-[10px] uppercase tracking-[0.1em] text-accent" title={smiles}>
        Docked pose · selectivity {sel}
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

/** Merge new docks into a list, replacing any existing dock of the same SMILES. */
function upsertDock(list: LigandDock[], dock: LigandDock): LigandDock[] {
  const idx = list.findIndex((d) => d.smiles === dock.smiles)
  if (idx === -1) return [...list, dock]
  const next = list.slice()
  next[idx] = dock
  return next
}

/**
 * RunViewerPage — route /runs/:id. The resistance-design workspace for one run:
 * the matched wild-type and mutant structures side by side, the WT→mutant pocket
 * delta the mutation opened up, and a generate → dock → rank loop. Claude proposes
 * drug-like molecules for the mutant pocket; docking one scores it into BOTH tracks
 * and overlays its poses in the two viewers; the selectivity board ranks them.
 */
export default function RunViewerPage() {
  const { id = '' } = useParams()
  const ctrlRef = useRef<AbortController | null>(null)

  const [run, setRun] = useState<Run | null>(null)
  const [runError, setRunError] = useState<string | null>(null)
  const [representation, setRepresentation] = useState(DEFAULT_REPRESENTATION)

  // Pocket analysis (slow: fpocket on both tracks) — kept separate from the run load.
  const [pockets, setPockets] = useState<RunPocketAnalysis | null>(null)
  const [pocketsStatus, setPocketsStatus] = useState<LoadStatus>('loading')
  const [pocketsError, setPocketsError] = useState<string | null>(null)

  const [candidates, setCandidates] = useState<Candidate[]>([])
  const [generating, setGenerating] = useState(false)
  const [generateError, setGenerateError] = useState<string | null>(null)

  const [docks, setDocks] = useState<LigandDock[]>([])
  const [dockState, setDockState] = useState<Record<string, CandidateDockState>>({})

  const [ranking, setRanking] = useState<Ranking | null>(null)
  const [rankingStatus, setRankingStatus] = useState<'idle' | 'loading' | 'done' | 'error'>('idle')
  const [rankingError, setRankingError] = useState<string | null>(null)

  // Which molecule's poses are shown in the viewers.
  const [activeSmiles, setActiveSmiles] = useState<string | null>(null)

  const refreshRanking = (signal?: AbortSignal) => {
    setRankingStatus('loading')
    getRunRanking(id, { signal })
      .then((r) => {
        setRanking(r)
        setRankingStatus('done')
      })
      .catch((e: unknown) => {
        if (signal?.aborted) return
        setRankingError(e instanceof Error ? e.message : 'Ranking failed')
        setRankingStatus('error')
      })
  }

  // Load the run + kick off pocket analysis on mount / id change.
  useEffect(() => {
    const ctrl = new AbortController()
    ctrlRef.current = ctrl

    setRun(null)
    setRunError(null)
    setCandidates([])
    setDocks([])
    setDockState({})
    setRanking(null)
    setRankingStatus('idle')
    setActiveSmiles(null)
    setPockets(null)
    setPocketsStatus('loading')
    setPocketsError(null)

    getRun(id, ctrl.signal)
      .then((r) => {
        setRun(r)
        setCandidates(r.candidates ?? [])
        const seededDocks = r.docks ?? []
        setDocks(seededDocks)
        // Reflect already-docked molecules as "done" in the candidate panel.
        setDockState(
          Object.fromEntries(
            seededDocks.map((d) => [d.smiles, { phase: 'done', selectivity: d.selectivity } as CandidateDockState]),
          ),
        )
        if (seededDocks.length > 0) refreshRanking(ctrl.signal)
      })
      .catch((e: unknown) => {
        if (!ctrl.signal.aborted) setRunError(e instanceof Error ? e.message : 'Failed to load run')
      })

    getRunPockets(id, ctrl.signal)
      .then((p) => {
        setPockets(p)
        setPocketsStatus('done')
      })
      .catch((e: unknown) => {
        if (!ctrl.signal.aborted) {
          setPocketsError(e instanceof Error ? e.message : 'Pocket analysis failed')
          setPocketsStatus('error')
        }
      })

    return () => ctrl.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  const handleGenerate = (n: number) => {
    setGenerating(true)
    setGenerateError(null)
    generateCandidates(id, n, ctrlRef.current?.signal)
      .then((fresh) => {
        // Append newly proposed molecules, de-duped by SMILES.
        setCandidates((prev) => {
          const seen = new Set(prev.map((c) => c.smiles))
          return [...prev, ...fresh.filter((c) => !seen.has(c.smiles))]
        })
        setGenerating(false)
      })
      .catch((e: unknown) => {
        if (ctrlRef.current?.signal.aborted) return
        setGenerateError(e instanceof Error ? e.message : 'Generation failed')
        setGenerating(false)
      })
  }

  const handleDock = (smiles: string) => {
    setDockState((prev) => ({ ...prev, [smiles]: { phase: 'docking' } }))
    dockRunLigand(id, smiles, ctrlRef.current?.signal)
      .then((dock) => {
        setDocks((prev) => upsertDock(prev, dock))
        setDockState((prev) => ({ ...prev, [smiles]: { phase: 'done', selectivity: dock.selectivity } }))
        setActiveSmiles(smiles)
        refreshRanking(ctrlRef.current?.signal ?? undefined)
      })
      .catch((e: unknown) => {
        if (ctrlRef.current?.signal.aborted) return
        setDockState((prev) => ({
          ...prev,
          [smiles]: { phase: 'error', error: e instanceof Error ? e.message : 'Docking failed' },
        }))
      })
  }

  // Highlight the resistance pocket's residues in BOTH viewers (WT and mutant
  // share a backbone frame + numbering, so the same residues apply to each).
  const highlight = useMemo<HighlightResidue[]>(() => {
    const ctx = pockets?.context
    if (!ctx) return []
    const pid = ctx.mutant_pocket.pocket_id
    const pocket = pockets?.mutant_pockets.find((p) => p.pocket_id === pid) ?? pockets?.mutant_pockets[0]
    if (!pocket) return []
    const idx = pocket.residue_indices ?? []
    const ch = pocket.residue_chains ?? []
    return idx.map((index, i) => ({ chain: ch[i] ?? ch[0] ?? '', index }))
  }, [pockets])

  const activeDock = activeSmiles ? docks.find((d) => d.smiles === activeSmiles) ?? null : null

  const hasStructures = !!run?.mutagenesis
  const wtUrl = hasStructures ? runStructureUrl(id, 'wt') : undefined
  const mutUrl = hasStructures ? runStructureUrl(id, 'mutant') : undefined

  const targetResidue = run?.mutagenesis
    ? `${run.mutagenesis.wild_type_residue}${run.mutagenesis.target_residue_number} → ${run.mutagenesis.mutant_residue}`
    : null
  const sourceLabel = run?.wt_structure
    ? run.wt_structure.pdb_id
      ? `PDB ${run.wt_structure.pdb_id}`
      : run.wt_structure.alphafold_id
        ? `AlphaFold ${run.wt_structure.alphafold_id}`
        : run.wt_structure.source
    : null

  return (
    <div className="flex min-h-screen flex-col bg-paper">
      <header className="sticky top-0 z-10 flex-none border-b border-hairline bg-paper/90 backdrop-blur-sm">
        <div className="mx-auto flex w-full max-w-6xl flex-col gap-3 px-6 py-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex min-w-0 items-center gap-4">
              <Link
                to="/runs"
                className="font-mono text-[11px] uppercase tracking-[0.1em] text-muted transition-colors hover:text-ink"
              >
                ← Runs
              </Link>
              <div className="min-w-0">
                <h1 className="flex items-center gap-2 truncate font-display text-xl font-medium text-ink">
                  {run?.uniprot_id || id}
                  {run?.mutation && (
                    <span className="flex-none rounded-full border border-accent/40 bg-accent-soft px-2 py-0.5 font-mono text-[11px] text-accent">
                      {run.mutation.raw}
                    </span>
                  )}
                </h1>
                <p className="truncate text-xs text-muted">Resistance-design run</p>
              </div>
            </div>
            {run?.status && (
              <span className="flex-none font-mono text-[10px] uppercase tracking-[0.12em] text-muted">
                {run.status}
              </span>
            )}
          </div>

          {(sourceLabel || targetResidue) && (
            <div className="flex flex-wrap gap-x-8 gap-y-2 border-t border-hairline pt-2.5">
              {sourceLabel && <MetaItem label="Source">{sourceLabel}</MetaItem>}
              {targetResidue && <MetaItem label="Mutation">{targetResidue}</MetaItem>}
            </div>
          )}
        </div>
      </header>

      {runError ? (
        <div className="flex flex-1 items-center justify-center p-6 text-center">
          <p className="font-mono text-sm text-conf-verylow">{runError}</p>
        </div>
      ) : !run ? (
        <div className="flex flex-1 items-center justify-center">
          <span className="animate-pulse font-mono text-xs uppercase tracking-[0.15em] text-muted">Loading…</span>
        </div>
      ) : (
        <div className="mx-auto flex w-full max-w-6xl flex-col gap-10 px-6 py-8">
          {/* ── Structures: WT + mutant side by side ── */}
          <section className="flex flex-col">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 className="font-display text-base font-medium text-ink">Structures</h2>
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

            {!hasStructures ? (
              <div className="mt-4 rounded-lg border border-dashed border-hairline bg-paper-deep/40 px-6 py-12 text-center">
                <p className="font-mono text-xs uppercase tracking-[0.12em] text-muted">
                  No mutant structure was built for this run
                </p>
              </div>
            ) : (
              <div className="mt-4 flex flex-col overflow-hidden rounded-lg border border-hairline bg-paper-deep">
                <div className="flex min-h-[420px] flex-col md:h-[56vh] md:min-h-[460px] md:flex-row">
                  <div className="relative flex min-h-[360px] flex-1 flex-col border-hairline max-md:border-b md:min-h-0 md:border-r">
                    <StructurePanel
                      url={wtUrl}
                      label="Wild type"
                      representation={representation}
                      highlight={highlight}
                      pose={activeDock?.wt_pose_pdb ?? null}
                    />
                    {activeDock && (
                      <PoseCaption
                        smiles={activeDock.smiles}
                        selectivity={activeDock.selectivity}
                        onClear={() => setActiveSmiles(null)}
                      />
                    )}
                  </div>
                  <div className="relative flex min-h-[360px] flex-1 flex-col md:min-h-0">
                    <StructurePanel
                      url={mutUrl}
                      label="Mutant"
                      representation={representation}
                      highlight={highlight}
                      pose={activeDock?.mutant_pose_pdb ?? null}
                    />
                    {activeDock && (
                      <PoseCaption
                        smiles={activeDock.smiles}
                        selectivity={activeDock.selectivity}
                        onClear={() => setActiveSmiles(null)}
                      />
                    )}
                  </div>
                </div>
              </div>
            )}
          </section>

          {/* ── What the mutation changed ── */}
          <section className="flex flex-col border-t border-hairline pt-6">
            <div className="mb-4 flex flex-col gap-1">
              <h2 className="font-display text-base font-medium text-ink">What the mutation changed</h2>
              <p className="text-xs text-muted">
                The mutant binding pocket and how it differs from the wild type — the resistance
                signal the generator designs against.
              </p>
            </div>
            <MutationDeltaPanel
              status={pocketsStatus}
              context={pockets?.context ?? null}
              error={pocketsError}
              mutation={run.mutation}
            />
          </section>

          {/* ── Generate & dock ── */}
          <section className="flex flex-col border-t border-hairline pt-6">
            <div className="mb-4 flex flex-col gap-1">
              <h2 className="font-display text-base font-medium text-ink">Generate &amp; dock</h2>
              <p className="text-xs text-muted">
                Ask Claude for drug-like molecules aimed at the mutant pocket, then dock one to score
                it into both tracks and rank it by selectivity.
              </p>
            </div>

            <div className="flex flex-col gap-8 lg:flex-row lg:gap-10">
              <div className="min-w-0 flex-1">
                <CandidatePanel
                  candidates={candidates}
                  generating={generating}
                  generateError={generateError}
                  onGenerate={handleGenerate}
                  dockState={dockState}
                  onDock={handleDock}
                  canGenerate={hasStructures}
                />
              </div>
              <aside className="flex-none lg:sticky lg:top-24 lg:w-80 lg:self-start">
                <SelectivityBoard
                  ranking={ranking}
                  status={rankingStatus}
                  error={rankingError}
                  activeSmiles={activeSmiles}
                  onSelect={setActiveSmiles}
                />
              </aside>
            </div>
          </section>
        </div>
      )}
    </div>
  )
}
