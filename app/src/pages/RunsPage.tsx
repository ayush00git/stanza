import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { createRun, listRuns, listRunsByProfile, type Run } from '../lib/api'
import { useActiveProfile } from '../lib/profile'
import Thinking, { RUN_PHASES } from '../components/Thinking'

type ListStatus = 'loading' | 'done' | 'error'

/** A few well-known resistance targets, to prefill the form with one click. */
const EXAMPLES: { label: string; uniprot: string; mutation: string }[] = [
  { label: 'KRAS G12C', uniprot: 'P01116', mutation: 'G12C' },
  { label: 'EGFR T790M', uniprot: 'P00533', mutation: 'T790M' },
  { label: 'ABL1 T315I', uniprot: 'P00519', mutation: 'T315I' },
]

/** Format an RFC3339 timestamp as a short local date, or "" if unparseable. */
function shortDate(ts: string): string {
  const d = new Date(ts)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString()
}

/**
 * RunsPage — route /runs. The launcher for the resistance-design flow: a form to
 * start a new run (UniProt target + resistance mutation) and a list of recent
 * runs. Creating a run acquires the wild-type structure and builds the mutant
 * pair server-side, then navigates into the viewer.
 */
export default function RunsPage() {
  const navigate = useNavigate()
  const active = useActiveProfile()

  const [uniprot, setUniprot] = useState('')
  const [mutation, setMutation] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)

  const [runs, setRuns] = useState<Run[]>([])
  const [status, setStatus] = useState<ListStatus>('loading')

  useEffect(() => {
    const ctrl = new AbortController()
    setStatus('loading')
    // Scope the list to the active profile when there is one; otherwise show all.
    const load = active ? listRunsByProfile(active.id, ctrl.signal) : listRuns(ctrl.signal)
    load
      .then((r) => {
        setRuns(r)
        setStatus('done')
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setStatus('error')
      })
    return () => ctrl.abort()
  }, [active])

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const uid = uniprot.trim()
    const mut = mutation.trim()
    if (!uid || !mut || creating) return
    setCreating(true)
    setCreateError(null)
    createRun({ uniprot_id: uid, mutation: mut, profile_id: active?.id })
      .then((run) => {
        // Stage-1 acquisition can fail while still returning the run (status
        // "error"); surface that here rather than opening an empty viewer.
        if (run.status === 'error') {
          setCreateError(run.error || 'Could not acquire a structure for this target.')
          setCreating(false)
          return
        }
        navigate(`/runs/${run.id}`)
      })
      .catch((err: unknown) => {
        setCreateError(err instanceof Error ? err.message : 'Could not create run')
        setCreating(false)
      })
  }

  return (
    <div className="min-h-screen bg-paper">
      {/* Header */}
      <header className="sticky top-0 z-10 border-b border-hairline bg-paper/90 backdrop-blur-sm">
        <div className="mx-auto flex w-full max-w-4xl items-center justify-between px-6 py-4">
          <Link to="/" className="font-display text-xl font-medium tracking-[-0.02em] text-ink">
            Stanza<span className="text-accent">.</span>
          </Link>
          <Link to="/" className="text-sm text-muted transition-colors hover:text-ink">
            ← Home
          </Link>
        </div>
      </header>

      <main className="mx-auto flex w-full max-w-4xl flex-col gap-12 px-6 py-12">
        {/* Launcher */}
        <section>
          <h1 className="font-display text-2xl font-medium text-ink">Resistance design</h1>
          <p className="mt-2 max-w-2xl text-sm text-muted">
            Start from a target and a resistance mutation. Stanza acquires the wild-type structure,
            builds the matched mutant, and drives a generate → dock → rank loop to find molecules that
            bind the mutant while sparing the wild type.
          </p>

          <form
            onSubmit={submit}
            className="mt-6 rounded-lg border border-hairline bg-paper-deep/40 p-5"
          >
            <div className="flex flex-col gap-4 sm:flex-row sm:items-end">
              <label className="flex min-w-0 flex-1 flex-col gap-1.5">
                <span className="text-xs text-muted">UniProt ID</span>
                <input
                  value={uniprot}
                  onChange={(e) => setUniprot(e.target.value)}
                  placeholder="P01116"
                  spellCheck={false}
                  className="rounded-md border border-hairline bg-paper px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-[var(--color-accent)]"
                />
              </label>
              <label className="flex flex-col gap-1.5 sm:w-40">
                <span className="text-xs text-muted">Mutation</span>
                <input
                  value={mutation}
                  onChange={(e) => setMutation(e.target.value)}
                  placeholder="G12C"
                  spellCheck={false}
                  className="rounded-md border border-hairline bg-paper px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-[var(--color-accent)]"
                />
              </label>
              <button
                type="submit"
                disabled={creating || !uniprot.trim() || !mutation.trim()}
                className="rounded-md border border-ink bg-ink px-4 py-2 text-sm font-medium text-paper transition-colors hover:bg-transparent hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
              >
                {creating ? 'Building…' : 'Start run'}
              </button>
            </div>

            {createError && <p className="mt-3 text-sm text-conf-verylow">{createError}</p>}

            {/* Example prefills */}
            <div className="mt-4 flex flex-wrap items-center gap-2">
              <span className="text-xs text-muted">Try</span>
              {EXAMPLES.map((ex) => (
                <button
                  key={ex.label}
                  type="button"
                  onClick={() => {
                    setUniprot(ex.uniprot)
                    setMutation(ex.mutation)
                  }}
                  className="rounded-full border border-hairline bg-paper px-2.5 py-0.5 text-xs text-ink transition-colors hover:border-[var(--color-accent)] hover:text-accent"
                >
                  {ex.label}
                </button>
              ))}
            </div>
          </form>
          {creating && <Thinking phases={RUN_PHASES} className="mt-4" />}
        </section>

        {/* Recent runs */}
        <section>
          <div className="mb-4">
            <h2 className="font-display text-base font-medium text-ink">Recent runs</h2>
            {active && (
              <p className="mt-1 text-xs text-muted">
                Showing runs for{' '}
                <Link to="/profile" className="text-accent hover:underline">
                  {active.name}
                </Link>
                .
              </p>
            )}
          </div>

          {status === 'error' ? (
            <p className="text-sm text-conf-verylow">Could not load runs.</p>
          ) : status === 'loading' ? (
            <p className="animate-pulse text-sm text-muted">Loading runs…</p>
          ) : runs.length === 0 ? (
            <div className="rounded-lg border border-dashed border-hairline bg-paper-deep/40 px-6 py-10 text-center">
              <p className="text-sm text-muted">No runs yet — start one above.</p>
            </div>
          ) : (
            <ul className="overflow-hidden rounded-md border border-hairline bg-paper">
              {runs.map((run) => (
                <li key={run.id}>
                  <Link
                    to={`/runs/${run.id}`}
                    className="flex items-center gap-3 border-b border-hairline px-4 py-3 transition-colors last:border-b-0 hover:bg-paper-deep"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-ink">{run.uniprot_id}</span>
                        <span className="rounded-full border border-accent/40 bg-accent-soft px-2 py-0.5 text-xs font-medium text-accent">
                          {run.mutation.raw}
                        </span>
                      </div>
                      <span className="text-xs text-muted">
                        {run.status.replace(/_/g, ' ')}
                        {run.created_at && ` · ${shortDate(run.created_at)}`}
                      </span>
                    </div>
                    <span className="flex-none text-sm text-muted">→</span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </section>
      </main>
    </div>
  )
}
