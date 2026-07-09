/** The four fitness terms, mirroring scoring.DefaultWeights(). Weights are
 *  normalised to sum to 1 before use, so they read directly as shares. */
const terms = [
  {
    name: 'Covalent feasibility',
    weight: 0.4,
    bar: 'bg-accent',
    swatch: 'bg-accent',
    body: 'Can the warhead actually reach the thiol and attack it from a sane angle? Measured geometry, in 0 to 1.',
  },
  {
    name: 'Mutant potency',
    weight: 0.3,
    bar: 'bg-ink',
    swatch: 'bg-ink',
    body: 'How tightly the molecule binds the resistant pocket.',
  },
  {
    name: 'Drug-likeness',
    weight: 0.2,
    bar: 'bg-muted/45',
    swatch: 'bg-muted/45',
    body: 'QED. A selective molecule nobody can dose is not a lead.',
  },
  {
    name: 'Selectivity margin',
    weight: 0.1,
    bar: 'bg-[var(--color-gain)]',
    swatch: 'bg-[var(--color-gain)]',
    body: 'The non-covalent gap between the two tracks — which nearly vanishes on a covalent target.',
  },
]

const rules = [
  {
    head: 'Normalised across the pool',
    body: 'Every term is z-scored over the round, so molecules compete against their peers rather than an absolute scale that shifts with the target.',
  },
  {
    head: 'A coin flip scores zero',
    body: 'When a molecule’s covalent call flips with the docking seed, its feasibility still shows on the board but contributes nothing to fitness. Ranking it on a median would launder noise into signal.',
  },
  {
    head: 'No warhead, no penalty',
    body: 'On a pool with no covalent molecules the term has no variance, so it drops out and the pool ranks exactly as it would have without it.',
  },
  {
    head: 'Ties break downward',
    body: 'Equal fitness resolves on selectivity, then on raw mutant affinity. The top twenty carry forward into the next round.',
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
            So four terms decide a molecule&rsquo;s fitness, not one. And on a
            covalent target the weighting inverts the obvious: the energetic
            margin between the tracks is worth least, because docking cannot see
            the bond that creates the selectivity. What separates a mutant binder
            from a wild-type binder there is not energy at all — it is whether
            the warhead can physically reach a thiol that only the mutant has.
          </p>
          <p className="mt-4 text-[0.95rem] leading-relaxed text-muted">
            That is measured, so that is what carries the weight.
          </p>
        </div>

        <div className="rounded-xl border border-hairline bg-paper p-8">
          <div className="flex items-baseline justify-between">
            <p className="font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
              Composite fitness
            </p>
            <p className="font-mono text-[11px] text-muted">
              tuned for a covalent target
            </p>
          </div>

          {/* The whole of fitness, as one bar. Segment width is the term's share. */}
          <div className="mt-5 flex h-2.5 w-full overflow-hidden rounded-full">
            {terms.map((term, i) => (
              <div
                key={term.name}
                className={`draw h-full ${term.bar}`}
                style={{
                  width: `${term.weight * 100}%`,
                  animationDelay: `${0.1 + i * 0.1}s`,
                }}
              />
            ))}
          </div>

          <ul className="mt-7 space-y-5">
            {terms.map((term) => (
              <li key={term.name} className="flex gap-3.5">
                <span
                  className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${term.swatch}`}
                  aria-hidden
                />
                <div className="min-w-0">
                  <div className="flex items-baseline justify-between gap-3">
                    <h3 className="font-display text-lg font-medium text-ink">
                      {term.name}
                    </h3>
                    <span className="font-mono text-sm tabular-nums text-accent">
                      {term.weight.toFixed(2)}
                    </span>
                  </div>
                  <p className="mt-1 text-[0.9rem] leading-relaxed text-muted">
                    {term.body}
                  </p>
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

      {/* How the pool is actually ranked, once the terms are computed. */}
      <div className="mt-16 border-t border-hairline pt-12">
        <h3 className="font-display text-2xl font-normal tracking-[-0.01em] text-ink">
          Ranking rules
        </h3>
        <dl className="mt-8 grid gap-x-10 gap-y-8 sm:grid-cols-2">
          {rules.map((rule) => (
            <div key={rule.head}>
              <dt className="font-display text-lg font-medium text-ink">
                {rule.head}
              </dt>
              <dd className="mt-1.5 text-[0.9rem] leading-relaxed text-muted">
                {rule.body}
              </dd>
            </div>
          ))}
        </dl>
      </div>
    </section>
  )
}
