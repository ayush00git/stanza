const terms = [
  {
    weight: 0.35,
    name: 'Mutant potency',
    body: 'How tightly the molecule binds the resistant pocket.',
  },
  {
    weight: 0.45,
    name: 'Selectivity margin',
    body: 'How much worse it binds the wild type. Weighted heaviest, because it is the point.',
  },
  {
    weight: 0.2,
    name: 'Drug-likeness',
    body: 'QED. A selective molecule nobody can dose is not a lead.',
  },
]

export default function Selectivity() {
  return (
    <section id="selectivity" className="mx-auto max-w-5xl px-6 py-20 sm:py-24">
      <div className="grid gap-14 lg:grid-cols-[1fr_1.1fr] lg:items-start">
        <div>
          <p className="font-mono text-xs uppercase tracking-[0.2em] text-accent">
            Scoring
          </p>
          <h2 className="mt-4 font-display text-3xl font-normal leading-tight tracking-[-0.01em] text-ink sm:text-4xl">
            Affinity is the wrong number.
          </h2>
          <p className="mt-6 text-[0.95rem] leading-relaxed text-muted">
            A molecule that binds the mutant pocket beautifully and the wild-type
            pocket just as beautifully is a toxin, not a therapy — it cannot tell
            a tumour from healthy tissue. Ranking on raw affinity finds those
            molecules first.
          </p>
          <p className="mt-4 text-[0.95rem] leading-relaxed text-muted">
            So Stanza ranks on a composite fitness instead. Each term is
            normalised across the round&rsquo;s pool, so molecules compete
            against their peers rather than against an absolute scale that shifts
            with every target.
          </p>
        </div>

        <div className="rounded-xl border border-hairline bg-paper p-8">
          <p className="font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
            Composite fitness
          </p>

          <ul className="mt-6 space-y-6">
            {terms.map((term) => (
              <li key={term.name}>
                <div className="flex items-baseline gap-4">
                  <span className="w-12 shrink-0 font-mono text-sm tabular-nums text-accent">
                    {term.weight.toFixed(2)}
                  </span>
                  <div>
                    <h3 className="font-display text-lg font-medium text-ink">
                      {term.name}
                    </h3>
                    <p className="mt-1 text-[0.9rem] leading-relaxed text-muted">
                      {term.body}
                    </p>
                  </div>
                </div>
                <div className="mt-3 ml-16 h-1 rounded-full bg-paper-deep">
                  <div
                    className="draw h-full rounded-full bg-accent/70"
                    style={{ width: `${term.weight * 100}%` }}
                  />
                </div>
              </li>
            ))}
          </ul>

          <div className="mt-8 border-t border-hairline pt-5">
            <p className="font-mono text-[11px] text-ink">
              selectivity = wt_score − mutant_score
            </p>
            <p className="mt-2 text-[0.85rem] leading-relaxed text-muted">
              Vina affinities are negative kcal/mol. Bind the mutant hard, the
              wild type weakly, and the margin goes positive.
            </p>
          </div>
        </div>
      </div>
    </section>
  )
}
