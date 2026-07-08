import { useEffect, useRef, useState } from 'react'
import {
  getChemblFragments,
  getDockStatus,
  submitDock,
  type DockStatus,
  type DockedPose,
  type Fragment,
  type Pocket,
} from '../../lib/api'

type LoadStatus = 'loading' | 'done' | 'error'

/** Interval between /dock/status polls, in ms. */
const POLL_INTERVAL_MS = 2000

/** How many fragments to reveal at a time (initial page and each subsequent reveal). */
const FRAGMENT_PAGE_SIZE = 6

/** ChEMBL compound report card, for linking a molecule to its source record. */
const CHEMBL_BASE_URL = 'https://www.ebi.ac.uk/chembl/compound_report_card/'

/** Per-fragment docking progress, keyed by ChEMBL id. */
type DockState = {
  /** 'submitting' precedes a job id; then it tracks the server DockStatus. */
  phase: 'submitting' | DockStatus
  jobId?: string
  bindingAffinity?: number
  error?: string
}

type Props = {
  pocket: Pocket
  uniprotId?: string
  /**
   * Protein source for docking — a local path or URL to the receptor PDB.
   * Falls back to `uniprotId` (sent as protein_pdb_id) when omitted.
   */
  proteinPdbPath?: string
  /**
   * When true the panel renders inline inside a pocket card: no full-width
   * `<section>` wrapper or large heading, and tightened paddings. When false or
   * omitted it keeps the standalone full-section layout.
   */
  compact?: boolean
  /** called when a dock completes, to lift the pose up for 3D display */
  onPose?: (pose: DockedPose) => void
}

const TERMINAL: DockStatus[] = ['done', 'error']

function isTerminal(phase: DockState['phase']): boolean {
  return phase === 'done' || phase === 'error'
}

/** Truncate a SMILES string for compact display. */
function truncateSmiles(smiles: string, max = 28): string {
  return smiles.length > max ? `${smiles.slice(0, max - 1)}…` : smiles
}

function StatusBadge({ state }: { state: DockState }) {
  const label =
    state.phase === 'submitting'
      ? 'Submitting'
      : state.phase.charAt(0).toUpperCase() + state.phase.slice(1)

  const tone =
    state.phase === 'error'
      ? 'bg-conf-verylow/15 text-ink'
      : state.phase === 'done'
        ? 'bg-accent-soft text-accent'
        : 'border border-hairline text-muted'

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.1em] ${tone}`}
    >
      {!isTerminal(state.phase) && (
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-current" />
      )}
      {label}
      {state.phase === 'done' && state.bindingAffinity != null && (
        <span className="tabular-nums">
          {state.bindingAffinity.toFixed(1)} kcal/mol
        </span>
      )}
    </span>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <span className="inline-flex items-baseline gap-1 font-mono text-[11px] text-muted">
      <span className="text-ink tabular-nums">{value}</span>
      <span className="uppercase tracking-[0.1em] text-[9px]">{label}</span>
    </span>
  )
}

/**
 * DockingPanel — a standalone panel that, for a given pocket, lists candidate
 * ChEMBL fragments and lets the user dock each one. Fragments are fetched on
 * mount and whenever the pocket changes. The "Dock" button submits an async
 * docking job and polls its status until it reaches a terminal state, showing
 * progress inline per fragment.
 *
 * Presentation + lifecycle are self-contained; wire it into a page by passing a
 * pocket (and optionally the receptor source and uniprot id).
 */
export default function DockingPanel({
  pocket,
  uniprotId,
  proteinPdbPath,
  compact,
  onPose,
}: Props) {
  const [status, setStatus] = useState<LoadStatus>('loading')
  const [fragments, setFragments] = useState<Fragment[]>([])
  const [error, setError] = useState<string | null>(null)
  const [docking, setDocking] = useState<Record<string, DockState>>({})
  // How many fragments are currently revealed; grows in FRAGMENT_PAGE_SIZE steps.
  const [visibleCount, setVisibleCount] = useState(FRAGMENT_PAGE_SIZE)

  // Track live poll timers so we can clear them on unmount / pocket change.
  const timers = useRef<Set<ReturnType<typeof setTimeout>>>(new Set())
  // Guards async callbacks against stale updates after unmount / pocket change.
  const activeRef = useRef(true)
  // Sentinel element at the end of the list; observed to auto-reveal more.
  const sentinelRef = useRef<HTMLLIElement | null>(null)
  // The fixed-height scrollable list container; also the observer root, so
  // auto-reveal triggers on scroll WITHIN the list rather than on page scroll.
  const listRef = useRef<HTMLUListElement | null>(null)

  // Fetch fragments on mount and whenever the pocket changes.
  useEffect(() => {
    activeRef.current = true
    const controller = new AbortController()
    setStatus('loading')
    setError(null)
    setFragments([])
    setDocking({})
    // Reset progressive reveal back to the first page for the new pocket.
    setVisibleCount(FRAGMENT_PAGE_SIZE)

    getChemblFragments(pocket.pocket_id, {
      sourceType: pocket.source_type,
      volume: pocket.volume,
      hydrophobicity: pocket.hydrophobicity,
      polarity: pocket.polarity,
      signal: controller.signal,
    })
      .then((frags) => {
        if (!activeRef.current) return
        setFragments(frags ?? [])
        setStatus('done')
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return
        setError(err instanceof Error ? err.message : 'Failed to load fragments.')
        setStatus('error')
      })

    const runningTimers = timers.current
    return () => {
      activeRef.current = false
      controller.abort()
      runningTimers.forEach((t) => clearTimeout(t))
      runningTimers.clear()
    }
  }, [pocket.pocket_id, pocket.source_type, pocket.volume, pocket.hydrophobicity, pocket.polarity])

  const setFragmentState = (chemblId: string, next: DockState) =>
    setDocking((prev) => ({ ...prev, [chemblId]: next }))

  function poll(chemblId: string, jobId: string) {
    getDockStatus(jobId)
      .then((res) => {
        if (!activeRef.current) return
        setFragmentState(chemblId, {
          phase: res.status,
          jobId,
          bindingAffinity: res.binding_affinity,
          error: res.error,
        })
        // On successful completion, lift the docked pose up so the page can
        // render it in the 3D Mol* viewer. Fires once per job: this branch is
        // only reached on a terminal 'done' status while the panel is active.
        if (
          res.status === 'done' &&
          typeof res.pose_pdb === 'string' &&
          res.pose_pdb.length > 0
        ) {
          // Look up the source fragment so the results leaderboard can label
          // this pose with a human-readable name and its SMILES.
          const frag = fragments.find((f) => f.chembl_id === chemblId)
          onPose?.({
            pdb: res.pose_pdb,
            source_type: pocket.source_type,
            pocket_id: pocket.pocket_id,
            chembl_id: chemblId,
            binding_affinity: res.binding_affinity,
            name: frag?.name,
            smiles: frag?.smiles,
          })
        }
        if (!TERMINAL.includes(res.status)) {
          const t = setTimeout(() => {
            timers.current.delete(t)
            poll(chemblId, jobId)
          }, POLL_INTERVAL_MS)
          timers.current.add(t)
        }
      })
      .catch((err: unknown) => {
        if (!activeRef.current) return
        setFragmentState(chemblId, {
          phase: 'error',
          jobId,
          error: err instanceof Error ? err.message : 'Status polling failed.',
        })
      })
  }

  function handleDock(frag: Fragment) {
    setFragmentState(frag.chembl_id, { phase: 'submitting' })
    submitDock({
      pocket_id: pocket.pocket_id,
      source_type: pocket.source_type,
      ligand_smiles: frag.smiles,
      protein_pdb_path: proteinPdbPath,
      protein_pdb_id: proteinPdbPath ? undefined : uniprotId,
    })
      .then((res) => {
        if (!activeRef.current) return
        setFragmentState(frag.chembl_id, { phase: 'pending', jobId: res.job_id })
        poll(frag.chembl_id, res.job_id)
      })
      .catch((err: unknown) => {
        if (!activeRef.current) return
        setFragmentState(frag.chembl_id, {
          phase: 'error',
          error: err instanceof Error ? err.message : 'Submission failed.',
        })
      })
  }

  // Fragments revealed so far, and whether more remain to reveal.
  const visibleFragments = fragments.slice(0, visibleCount)
  const hasMore = visibleCount < fragments.length
  const remaining = fragments.length - visibleCount

  /** Reveal the next page of fragments. */
  const revealMore = () =>
    setVisibleCount((c) => Math.min(c + FRAGMENT_PAGE_SIZE, fragments.length))

  // Observe the sentinel: when it scrolls into view, reveal the next page.
  // Guarded by a ref and disconnected on cleanup / when the sentinel unmounts.
  useEffect(() => {
    if (!hasMore) return
    const node = sentinelRef.current
    if (!node) return

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) revealMore()
      },
      // Observe within the fixed-height scroll container, not the viewport.
      { root: listRef.current ?? null },
    )
    observer.observe(node)

    return () => observer.disconnect()
    // Re-run when the reveal position or fragment set changes so a fresh
    // sentinel (further down the list) gets observed.
  }, [hasMore, visibleCount, fragments.length])

  // Shared body: loading / error / empty states and the progressive list.
  const body = (
    <>
      {status === 'loading' && (
        <p className="mt-6 animate-pulse font-mono text-xs uppercase tracking-[0.15em] text-muted">
          Fetching candidate fragments from ChEMBL…
        </p>
      )}

      {status === 'error' && (
        <p className="mt-6 font-mono text-sm text-conf-verylow">{error}</p>
      )}

      {status === 'done' && fragments.length === 0 && (
        <p className="mt-6 text-sm text-muted">
          No candidate fragments matched this pocket.
        </p>
      )}

      {status === 'done' && fragments.length > 0 && (
        <ul
          ref={listRef}
          className="mt-6 max-h-[26rem] overflow-y-auto rounded-md border border-hairline bg-paper"
        >
          {visibleFragments.map((frag) => {
            const state = docking[frag.chembl_id]
            const busy = state != null && !isTerminal(state.phase)
            return (
              <li
                key={frag.chembl_id}
                className={`flex flex-col gap-3 border-b border-hairline last:border-b-0 sm:flex-row sm:items-start sm:justify-between ${
                  compact ? 'px-3 py-3' : 'px-4 py-3.5'
                }`}
              >
                <div className="min-w-0 flex-1">
                  {/* Identity — id + name on one calm line. */}
                  <div className="flex items-baseline gap-2">
                    <a
                      href={`${CHEMBL_BASE_URL}${frag.chembl_id}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="flex-none font-mono text-xs text-accent transition-colors hover:underline"
                      title={`View ${frag.chembl_id} on ChEMBL`}
                    >
                      {frag.chembl_id} ↗
                    </a>
                    {frag.name && (
                      <span className="truncate text-sm text-ink">{frag.name}</span>
                    )}
                  </div>

                  {/* Quiet, labelled metrics. */}
                  <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1">
                    <Metric label="MW" value={frag.mol_weight.toFixed(1)} />
                    <Metric label="logP" value={frag.logp.toFixed(2)} />
                    <Metric label="sim" value={frag.similarity_score.toFixed(2)} />
                  </div>

                  {/* SMILES — de-emphasised on its own line, full value on hover. */}
                  <p
                    className="mt-1.5 truncate font-mono text-[10px] text-muted/80"
                    title={frag.smiles}
                  >
                    {truncateSmiles(frag.smiles)}
                  </p>

                  {state?.phase === 'error' && state.error && (
                    <p className="mt-1.5 font-mono text-[11px] text-conf-verylow">
                      {state.error}
                    </p>
                  )}

                  {/* Nudge the user toward the 3D viewer once a pose is emitted. */}
                  {state?.phase === 'done' && (
                    <p className="mt-1.5 font-mono text-[10px] text-accent">
                      Pose shown in 3D →
                    </p>
                  )}
                </div>

                {/* Status + action, right-aligned with a steady width. */}
                <div className="flex flex-none items-center gap-2 sm:w-40 sm:flex-col sm:items-end sm:gap-2">
                  {state && <StatusBadge state={state} />}
                  <button
                    type="button"
                    onClick={() => handleDock(frag)}
                    disabled={busy}
                    className="w-full max-w-[7rem] rounded-md border border-hairline bg-paper-deep px-3 py-1.5 font-mono text-[11px] uppercase tracking-[0.1em] text-ink transition-colors hover:border-[var(--color-accent)] hover:text-accent disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {state && isTerminal(state.phase) ? 'Re-dock' : 'Dock'}
                  </button>
                </div>
              </li>
            )
          })}

          {/* Sentinel + fallback button: only while more fragments remain. */}
          {hasMore && (
            <li
              ref={sentinelRef}
              className={`flex justify-center ${compact ? 'px-3 py-2.5' : 'px-4 py-3'}`}
            >
              <button
                type="button"
                onClick={revealMore}
                className="rounded-md border border-hairline bg-paper-deep px-3 py-1.5 font-mono text-[11px] uppercase tracking-[0.1em] text-muted transition-colors hover:border-[var(--color-accent)] hover:text-accent"
              >
                Load {Math.min(FRAGMENT_PAGE_SIZE, remaining)} more
              </button>
            </li>
          )}
        </ul>
      )}
    </>
  )

  // Compact mode: lightweight inline container with a small mono label.
  if (compact) {
    return (
      <div className="mt-4">
        <span className="font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
          Fragment docking · ChEMBL
        </span>
        {body}
      </div>
    )
  }

  return (
    <section className="mx-auto w-full max-w-5xl px-6 py-8">
      <div className="flex items-baseline justify-between">
        <h2 className="font-display text-xl font-medium text-ink">
          Fragment docking
        </h2>
        <span className="font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
          ChEMBL · pocket P{pocket.pocket_id}
        </span>
      </div>

      {body}
    </section>
  )
}
