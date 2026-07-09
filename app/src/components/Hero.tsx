import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import DualTrack from './DualTrack'
import { createRun } from '../lib/api'
import { useActiveProfile } from '../lib/profile'

// The target the card launches. These are the run's real inputs, not decoration:
// the button below posts exactly this to POST /runs.
const TARGET = {
  uniprot: 'P01116',
  mutation: 'G12C',
  siteHint: 'switch-II',
}

// KRAS residues 5–20. Position 12 is the glycine that mutates to cysteine in
// G12C — the substitution sotorasib and adagrasib were built against.
const fragment = 'KLVVVGAGGVGKSALT'
const startResidue = 5
const mutatedResidue = 12

// Curated facts for this site, mirroring services/known_sites.go.
const spec = [
  { label: 'UniProt', value: 'P01116' },
  { label: 'Organism', value: 'H. sapiens' },
  { label: 'Length', value: '189 aa' },
  { label: 'Site', value: 'Switch-II' },
  { label: 'Template', value: 'PDB 6OIM' },
  { label: 'Warhead', value: 'Acrylamide' },
]

/** The mutation, set in the sequence where it actually happens. Two rows: the
 *  residues, and the ticks. The substituted residue carries the accent. */
function MutationStrip() {
  return (
    <div className="flex flex-wrap">
      {fragment.split('').map((aa, i) => {
        const pos = startResidue + i
        const mutated = pos === mutatedResidue
        return (
          <div key={i} className="flex w-[1.3rem] flex-col items-center">
            <span
              className={`font-mono text-sm leading-none ${
                mutated
                  ? 'rounded-sm bg-accent-soft px-1 py-0.5 font-medium text-accent'
                  : 'py-0.5 text-ink'
              }`}
              title={mutated ? 'Gly12 → Cys12' : undefined}
            >
              {mutated ? 'C' : aa}
            </span>
            <span
              className={`mt-1.5 h-3 font-mono text-[10px] leading-none tabular-nums ${
                mutated ? 'text-accent' : 'text-muted'
              }`}
            >
              {mutated || pos % 10 === 0 ? pos : ''}
            </span>
          </div>
        )
      })}
    </div>
  )
}

export default function Hero() {
  const navigate = useNavigate()
  const profile = useActiveProfile()
  const [starting, setStarting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Stage 1–2 (structure acquisition + mutagenesis) run synchronously server-side,
  // so this takes a few seconds before the viewer has anything to show.
  const start = () => {
    if (starting) return
    setStarting(true)
    setError(null)
    createRun({
      uniprot_id: TARGET.uniprot,
      mutation: TARGET.mutation,
      site_hint: TARGET.siteHint,
      profile_id: profile?.id,
    })
      .then((run) => {
        // Acquisition can fail while still returning a run; don't open an empty viewer.
        if (run.status === 'error') {
          setError(run.error || 'Could not acquire a structure for this target.')
          setStarting(false)
          return
        }
        navigate(`/runs/${run.id}`)
      })
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : 'Could not start the run.')
        setStarting(false)
      })
  }

  return (
    <section id="top" className="mx-auto max-w-5xl px-6 pt-16 pb-20 sm:pt-24">
      <div className="grid gap-14 lg:grid-cols-[1.05fr_1fr] lg:items-center">
        <div>
          <h1 className="rise font-display text-[2.7rem] font-normal leading-[1.06] tracking-[-0.015em] text-ink sm:text-[4.1rem]">
            Resistance-aware
            <br />
            drug design.
          </h1>

          {/* The thesis, in the display face's italic — the one claim everything
              downstream has to earn. */}
          <p
            className="rise mt-6 font-display text-[1.4rem] italic leading-snug tracking-[-0.01em] text-accent sm:text-[1.7rem]"
            style={{ animationDelay: '0.12s' }}
          >
            Bind the mutant. Spare the wild type.
          </p>

          <p
            className="rise mt-7 max-w-md text-lg leading-relaxed text-muted"
            style={{ animationDelay: '0.2s' }}
          >
            When a target mutates, the drug stops working. Stanza takes the
            mutation as its starting input, rebuilds the resistant pocket, and
            has Claude design molecules against it — docking every candidate
            into the mutant <em>and</em> the wild type to find the ones that can
            tell them apart.
          </p>

          <div
            className="rise mt-10 flex flex-wrap items-center gap-4"
            style={{ animationDelay: '0.28s' }}
          >
            <Link
              to="/runs"
              className="rounded-full bg-ink px-6 py-3 text-sm font-medium text-paper transition-transform hover:-translate-y-0.5"
            >
              Start a resistance run
            </Link>
            <a
              href="#pipeline"
              className="text-sm font-medium text-ink underline decoration-hairline decoration-2 underline-offset-4 transition-colors hover:decoration-accent"
            >
              See how it works
            </a>
          </div>
        </div>

        {/* Signature: one molecule, both tracks, the margin between them. */}
        <figure
          className="rise overflow-hidden rounded-xl border border-hairline bg-paper shadow-[0_1px_0_rgba(18,22,28,0.02),0_18px_40px_-28px_rgba(18,22,28,0.35)]"
          style={{ animationDelay: '0.36s' }}
        >
          <figcaption className="flex items-baseline justify-between gap-4 border-b border-hairline px-6 py-4">
            <span className="font-display text-lg font-medium text-ink">
              KRAS <span className="text-accent">G12C</span>
            </span>
            <span className="whitespace-nowrap font-mono text-[10px] uppercase tracking-[0.15em] text-muted">
              Example run
            </span>
          </figcaption>

          <div className="border-b border-hairline px-6 py-4">
            <dl className="grid grid-cols-3 gap-x-4 gap-y-3">
              {spec.map((item) => (
                <div key={item.label}>
                  <dt className="font-mono text-[10px] uppercase tracking-[0.1em] text-muted">
                    {item.label}
                  </dt>
                  <dd className="mt-0.5 font-mono text-[0.8rem] text-ink">
                    {item.value}
                  </dd>
                </div>
              ))}
            </dl>
          </div>

          <div className="border-b border-hairline px-6 py-4">
            <MutationStrip />
          </div>

          <div className="px-6 py-4">
            <DualTrack wt={-6.8} mutant={-9.4} />
          </div>

          {/* The covalent bond docking cannot score, and the geometry that earns it. */}
          <div className="flex items-baseline justify-between gap-4 border-t border-hairline px-6 py-3">
            <span className="font-mono text-[10px] uppercase tracking-[0.15em] text-muted">
              Covalent
            </span>
            <span className="text-right text-[0.75rem] text-muted">
              Warhead <span className="font-mono tabular-nums text-ink">4.5 Å</span>{' '}
              from the Cys12 thiol
            </span>
          </div>

          <div className="border-t border-hairline bg-paper-deep/40 px-6 py-4">
            <button
              type="button"
              onClick={start}
              disabled={starting}
              className="w-full rounded-full bg-ink px-5 py-2.5 text-sm font-medium text-paper transition-colors hover:bg-accent disabled:cursor-wait disabled:opacity-70"
            >
              {starting
                ? 'Acquiring structure…'
                : `Run ${TARGET.uniprot} · ${TARGET.mutation}`}
            </button>

            {error && (
              <p
                role="alert"
                className="mt-2.5 text-[0.75rem] leading-relaxed text-[var(--color-danger)]"
              >
                {error}
              </p>
            )}
          </div>
        </figure>
      </div>
    </section>
  )
}
