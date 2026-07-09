import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import {
  createProfile,
  listProfiles,
  listRunsByProfile,
  type Profile,
  type Run,
} from '../lib/api'
import {
  clearActiveProfile,
  setActiveProfile,
  useActiveProfile,
} from '../lib/profile'

type ListStatus = 'loading' | 'done' | 'error'

/** Format an RFC3339 timestamp as a short local date, or "" if unparseable. */
function shortDate(ts: string): string {
  const d = new Date(ts)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString()
}

/**
 * ProfilePage — route /profile. A minimal researcher profile: there's no real
 * auth, a profile just lets someone track their run history. Two modes: create /
 * pick a profile when none is active, or a summary + run history when one is.
 */
export default function ProfilePage() {
  const active = useActiveProfile()

  return (
    <div className="min-h-screen bg-paper">
      <header className="sticky top-0 z-10 border-b border-hairline bg-paper/90 backdrop-blur-sm">
        <div className="mx-auto flex w-full max-w-4xl items-center justify-between px-6 py-4">
          <Link
            to="/"
            className="font-display text-xl font-medium tracking-[-0.02em] text-ink"
          >
            Stanza<span className="text-accent">.</span>
          </Link>
          <Link to="/" className="text-sm text-muted transition-colors hover:text-ink">
            ← Home
          </Link>
        </div>
      </header>

      <main className="mx-auto flex w-full max-w-4xl flex-col gap-12 px-6 py-12">
        {active ? <ActiveProfileView profile={active} /> : <CreateProfileView />}
      </main>
    </div>
  )
}

/* ── Create / pick a profile ──────────────────────────────────────────────── */

function CreateProfileView() {
  const navigate = useNavigate()

  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [institution, setInstitution] = useState('')
  const [field, setField] = useState('')
  const [orcid, setOrcid] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)

  const [profiles, setProfiles] = useState<Profile[]>([])
  const [status, setStatus] = useState<ListStatus>('loading')

  useEffect(() => {
    const ctrl = new AbortController()
    listProfiles(ctrl.signal)
      .then((p) => {
        setProfiles(p)
        setStatus('done')
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setStatus('error')
      })
    return () => ctrl.abort()
  }, [])

  const pick = (p: Profile) => {
    setActiveProfile(p)
    navigate('/runs')
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (creating) return
    setCreating(true)
    setCreateError(null)
    // Only non-empty optional fields are sent; name goes through as-is (the
    // server requires it) — we don't hard-block on it client-side.
    const trim = (s: string) => {
      const t = s.trim()
      return t ? t : undefined
    }
    createProfile({
      name: name.trim(),
      email: trim(email),
      institution: trim(institution),
      field: trim(field),
      orcid: trim(orcid),
    })
      .then((p) => {
        setActiveProfile(p)
        navigate('/runs')
      })
      .catch((err: unknown) => {
        const msg = err instanceof Error ? err.message : 'Could not create profile'
        // The server returns 503 when no database is configured — surface a
        // friendly hint rather than the raw error.
        setCreateError(
          /database|DATABASE_URL|503/i.test(msg)
            ? 'Profiles need the database — set DATABASE_URL on the server.'
            : msg,
        )
        setCreating(false)
      })
  }

  return (
    <>
      <section>
        <h1 className="font-display text-2xl font-medium text-ink">
          Create your profile
        </h1>
        <p className="mt-2 max-w-2xl text-sm text-muted">
          A profile keeps your run history in one place. There's no sign-up and
          nothing is verified — just enough to tell your runs apart.
        </p>

        <form
          onSubmit={submit}
          className="mt-6 rounded-lg border border-hairline bg-paper-deep/40 p-5"
        >
          <div className="flex flex-col gap-4">
            <label className="flex flex-col gap-1.5">
              <span className="text-xs text-muted">
                Name <span className="text-muted/70">· required</span>
              </span>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Ada Lovelace"
                className="rounded-md border border-hairline bg-paper px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-[var(--color-accent)]"
              />
            </label>

            <div className="grid gap-4 sm:grid-cols-2">
              <label className="flex flex-col gap-1.5">
                <span className="text-xs text-muted">Email</span>
                <input
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="ada@example.org"
                  spellCheck={false}
                  className="rounded-md border border-hairline bg-paper px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-[var(--color-accent)]"
                />
              </label>
              <label className="flex flex-col gap-1.5">
                <span className="text-xs text-muted">Institution</span>
                <input
                  value={institution}
                  onChange={(e) => setInstitution(e.target.value)}
                  placeholder="Analytical Society"
                  className="rounded-md border border-hairline bg-paper px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-[var(--color-accent)]"
                />
              </label>
              <label className="flex flex-col gap-1.5">
                <span className="text-xs text-muted">Research field</span>
                <input
                  value={field}
                  onChange={(e) => setField(e.target.value)}
                  placeholder="Computational chemistry"
                  className="rounded-md border border-hairline bg-paper px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-[var(--color-accent)]"
                />
              </label>
              <label className="flex flex-col gap-1.5">
                <span className="text-xs text-muted">ORCID</span>
                <input
                  value={orcid}
                  onChange={(e) => setOrcid(e.target.value)}
                  placeholder="0000-0000-0000-0000"
                  spellCheck={false}
                  className="rounded-md border border-hairline bg-paper px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-[var(--color-accent)]"
                />
              </label>
            </div>

            <div>
              <button
                type="submit"
                disabled={creating}
                className="rounded-md border border-ink bg-ink px-4 py-2 text-sm font-medium text-paper transition-colors hover:bg-transparent hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
              >
                {creating ? 'Creating…' : 'Create profile'}
              </button>
            </div>
          </div>

          {createError && (
            <p className="mt-3 text-sm text-conf-verylow">{createError}</p>
          )}
        </form>
      </section>

      {/* Continue as an existing profile */}
      <section>
        <h2 className="mb-4 font-display text-base font-medium text-ink">
          Continue as an existing profile
        </h2>

        {status === 'error' ? (
          <p className="text-sm text-conf-verylow">Could not load profiles.</p>
        ) : status === 'loading' ? (
          <p className="animate-pulse text-sm text-muted">Loading profiles…</p>
        ) : profiles.length === 0 ? (
          <div className="rounded-lg border border-dashed border-hairline bg-paper-deep/40 px-6 py-10 text-center">
            <p className="text-sm text-muted">
              No profiles yet — create one above to get started.
            </p>
          </div>
        ) : (
          <ul className="overflow-hidden rounded-md border border-hairline bg-paper">
            {profiles.map((p) => (
              <li key={p.id}>
                <button
                  type="button"
                  onClick={() => pick(p)}
                  className="flex w-full items-center gap-3 border-b border-hairline px-4 py-3 text-left transition-colors last:border-b-0 hover:bg-paper-deep"
                >
                  <div className="min-w-0 flex-1">
                    <span className="text-sm font-medium text-ink">{p.name}</span>
                    <span className="block text-xs text-muted">
                      {[p.institution, p.field].filter(Boolean).join(' · ') ||
                        (p.created_at && `Joined ${shortDate(p.created_at)}`)}
                    </span>
                  </div>
                  <span className="flex-none text-sm text-muted">→</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </>
  )
}

/* ── Active profile: summary + run history ────────────────────────────────── */

function ActiveProfileView({ profile }: { profile: Profile }) {
  const [runs, setRuns] = useState<Run[]>([])
  const [status, setStatus] = useState<ListStatus>('loading')

  useEffect(() => {
    const ctrl = new AbortController()
    setStatus('loading')
    listRunsByProfile(profile.id, ctrl.signal)
      .then((r) => {
        setRuns(r)
        setStatus('done')
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setStatus('error')
      })
    return () => ctrl.abort()
  }, [profile.id])

  const details: { label: string; value?: string }[] = [
    { label: 'Email', value: profile.email },
    { label: 'Institution', value: profile.institution },
    { label: 'Research field', value: profile.field },
    { label: 'ORCID', value: profile.orcid },
  ]
  const shown = details.filter((d) => d.value)

  return (
    <>
      <section>
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <h1 className="font-display text-2xl font-medium text-ink">
              {profile.name}
            </h1>
            {profile.created_at && (
              <p className="mt-1 text-sm text-muted">
                Profile since {shortDate(profile.created_at)}
              </p>
            )}
          </div>
          <button
            type="button"
            onClick={clearActiveProfile}
            className="flex-none rounded-md border border-hairline bg-paper px-3 py-1.5 text-sm text-ink transition-colors hover:border-[var(--color-accent)] hover:text-accent"
          >
            Switch / create another
          </button>
        </div>

        {shown.length > 0 && (
          <dl className="mt-6 grid gap-4 rounded-lg border border-hairline bg-paper-deep/40 p-5 sm:grid-cols-2">
            {shown.map((d) => (
              <div key={d.label} className="flex flex-col gap-0.5">
                <dt className="text-xs text-muted">{d.label}</dt>
                <dd className="text-sm text-ink">{d.value}</dd>
              </div>
            ))}
          </dl>
        )}
      </section>

      {/* This profile's runs */}
      <section>
        <h2 className="mb-4 font-display text-base font-medium text-ink">Your runs</h2>

        {status === 'error' ? (
          <p className="text-sm text-muted">Could not load your runs right now.</p>
        ) : status === 'loading' ? (
          <p className="animate-pulse text-sm text-muted">Loading runs…</p>
        ) : runs.length === 0 ? (
          <div className="rounded-lg border border-dashed border-hairline bg-paper-deep/40 px-6 py-10 text-center">
            <p className="text-sm text-muted">
              No runs yet —{' '}
              <Link to="/runs" className="text-accent hover:underline">
                start one
              </Link>
              .
            </p>
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
                      <span className="text-sm font-medium text-ink">
                        {run.uniprot_id}
                      </span>
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
    </>
  )
}
