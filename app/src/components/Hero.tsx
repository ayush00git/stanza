import { Link } from 'react-router-dom'
import DualTrack from './DualTrack'

// KRAS residues 5–20. Position 12 is the glycine that mutates to cysteine in
// G12C — the most common oncogenic KRAS substitution, and the one sotorasib and
// adagrasib were built against.
const fragment = 'KLVVVGAGGVGKSALT'
const startResidue = 5
const mutatedResidue = 12

const readout = [
  { label: 'UniProt', value: 'P01116' },
  { label: 'Mutation', value: 'G12C' },
  { label: 'Site', value: 'Switch-II' },
  { label: 'Warhead', value: 'Acrylamide' },
]

/** The mutation, set in the sequence where it actually happens. */
function MutationStrip() {
  return (
    <div className="flex flex-wrap gap-y-3">
      {fragment.split('').map((aa, i) => {
        const pos = startResidue + i
        const mutated = pos === mutatedResidue
        return (
          <div key={i} className="flex w-[1.35rem] flex-col items-center">
            <span
              className={`font-mono text-sm leading-none ${
                mutated
                  ? 'text-muted line-through decoration-hairline'
                  : 'text-ink'
              }`}
            >
              {aa}
            </span>
            <span
              className={`mt-1.5 font-mono text-sm leading-none ${
                mutated ? 'text-accent' : 'text-transparent'
              }`}
              aria-hidden={!mutated}
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
  return (
    <section id="top" className="mx-auto max-w-5xl px-6 pt-16 pb-20 sm:pt-24">
      <p className="rise mb-8 font-mono text-xs uppercase tracking-[0.25em] text-accent">
        Resistance-aware drug design
      </p>

      <div className="grid gap-14 lg:grid-cols-[1.05fr_1fr] lg:items-center">
        <div className="rise" style={{ animationDelay: '0.1s' }}>
          <h1 className="font-display text-[2.6rem] font-normal leading-[1.08] tracking-[-0.02em] text-ink sm:text-6xl">
            Bind the mutant.
            <br />
            Spare the wild type.
          </h1>
          <p className="mt-8 max-w-md text-lg leading-relaxed text-muted">
            When a target mutates, the drug stops working. Stanza takes the
            mutation as its starting input, rebuilds the resistant pocket, and
            has Claude design molecules against it — docking every candidate
            into the mutant <em>and</em> the wild type to find the ones that can
            tell them apart.
          </p>

          <div className="mt-10 flex flex-wrap items-center gap-4">
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
          className="rise rounded-xl border border-hairline bg-paper p-6 shadow-[0_1px_0_rgba(18,22,28,0.02),0_18px_40px_-28px_rgba(18,22,28,0.35)]"
          style={{ animationDelay: '0.25s' }}
        >
          <figcaption className="flex items-baseline justify-between border-b border-hairline pb-4">
            <span className="font-display text-lg font-medium text-ink">
              KRAS <span className="text-accent">G12C</span>
            </span>
            <span className="font-mono text-xs uppercase tracking-[0.15em] text-accent">
              Run
            </span>
          </figcaption>

          <dl className="mt-4 grid grid-cols-4 gap-2">
            {readout.map((item) => (
              <div key={item.label}>
                <dt className="font-mono text-[10px] uppercase tracking-[0.1em] text-muted">
                  {item.label}
                </dt>
                <dd className="mt-1 font-mono text-sm text-ink">{item.value}</dd>
              </div>
            ))}
          </dl>

          <p className="mt-6 mb-3 font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
            Glycine 12 → cysteine
          </p>
          <MutationStrip />

          <div className="mt-7 border-t border-hairline pt-6">
            <DualTrack wt={-6.8} mutant={-9.4} />
          </div>
        </figure>
      </div>
    </section>
  )
}
