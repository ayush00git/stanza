import Sequence from './Sequence'

// A representative kinase-domain fragment for the hero target.
const fragment = 'KVLQKSQGQKTPMKSSPFRRLGGSAKQTEGLTKQVLNMYGKSPFKRLGGDAGKTPYQVLE'
const startResidue = 133

// Plausible pLDDT profile: termini less certain, ordered core very high.
const confidence = Array.from(fragment, (_, i) => {
  const t = i / (fragment.length - 1)
  const core = 1 - Math.pow(2 * t - 1, 2) // 0 at ends, 1 in the middle
  const ripple = 6 * Math.sin(i * 0.9)
  return Math.round(58 + core * 36 + ripple)
})

const meanPlddt = (
  confidence.reduce((a, b) => a + b, 0) / confidence.length
).toFixed(1)

const readout = [
  { label: 'UniProt', value: 'O14965' },
  { label: 'Organism', value: 'H. sapiens' },
  { label: 'Length', value: '403 aa' },
  { label: 'Mean pLDDT', value: meanPlddt },
]

export default function Hero() {
  return (
    <section id="top" className="mx-auto max-w-5xl px-6 pt-16 pb-20 sm:pt-24">
      <p className="rise mb-8 font-mono text-xs uppercase tracking-[0.25em] text-accent">
        Structure-guided drug discovery
      </p>

      <div className="grid gap-14 lg:grid-cols-[1.05fr_1fr] lg:items-center">
        <div className="rise" style={{ animationDelay: '0.1s' }}>
          <h1 className="font-display text-[2.6rem] font-normal leading-[1.08] tracking-[-0.02em] text-ink sm:text-6xl">
            From protein target to lead candidate, on one pipeline.
          </h1>
          <p className="mt-8 max-w-md text-lg leading-relaxed text-muted">
            Stanza pulls structures from AlphaFold and sequences from UniProt,
            maps the druggable pockets, and ranks candidate molecules — so your
            team spends its time on the chemistry that matters.
          </p>

          <div className="mt-10 flex flex-wrap items-center gap-4">
            <a
              href="#search"
              className="rounded-full bg-ink px-6 py-3 text-sm font-medium text-paper transition-transform hover:-translate-y-0.5"
            >
              Search a target
            </a>
            <a
              href="#pipeline"
              className="text-sm font-medium text-ink underline decoration-hairline decoration-2 underline-offset-4 transition-colors hover:decoration-accent"
            >
              See how it works
            </a>
          </div>
        </div>

        {/* Signature: the live target card */}
        <figure
          className="rise rounded-xl border border-hairline bg-paper p-6 shadow-[0_1px_0_rgba(18,22,28,0.02),0_18px_40px_-28px_rgba(18,22,28,0.35)]"
          style={{ animationDelay: '0.25s' }}
        >
          <figcaption className="flex items-baseline justify-between border-b border-hairline pb-4">
            <span className="font-display text-lg font-medium text-ink">
              Aurora kinase A
            </span>
            <span className="font-mono text-xs uppercase tracking-[0.15em] text-accent">
              Target
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
            Residues {startResidue}–{startResidue + fragment.length - 1}
          </p>
          <Sequence
            residues={fragment}
            confidence={confidence}
            startResidue={startResidue}
          />
        </figure>
      </div>
    </section>
  )
}
