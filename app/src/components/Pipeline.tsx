type Stage = {
  n: string
  title: string
  body: string
  source?: string
}

const stages: Stage[] = [
  {
    n: '01',
    title: 'Target intake',
    body: 'Resolve any accession to its canonical sequence, isoforms, and functional annotations.',
    source: 'UniProt',
  },
  {
    n: '02',
    title: 'Structure prediction',
    body: 'Fetch predicted models for monomers and dimers, carrying per-residue pLDDT confidence through every step.',
    source: 'AlphaFold',
  },
  {
    n: '03',
    title: 'Pocket detection',
    body: 'Locate druggable pockets on the predicted surface and score them for depth, shape, and accessibility.',
  },
  {
    n: '04',
    title: 'Candidate screening',
    body: 'Dock compound libraries against each pocket and filter by predicted affinity and drug-likeness.',
  },
  {
    n: '05',
    title: 'Ranking',
    body: 'Rank the survivors into a shortlist your chemists can act on, with the evidence behind each call.',
  },
]

export default function Pipeline() {
  return (
    <section id="pipeline" className="border-t border-hairline bg-paper-deep/50">
      <div className="mx-auto max-w-5xl px-6 py-20 sm:py-24">
        <div className="max-w-xl">
          <p className="font-mono text-xs uppercase tracking-[0.2em] text-accent">
            The pipeline
          </p>
          <h2 className="mt-4 font-display text-3xl font-normal leading-tight tracking-[-0.01em] text-ink sm:text-4xl">
            Five stages, from accession to shortlist.
          </h2>
        </div>

        <ol className="mt-14 border-t border-hairline">
          {stages.map((stage) => (
            <li
              key={stage.n}
              className="grid gap-4 border-b border-hairline py-6 sm:grid-cols-[4rem_1fr_auto] sm:items-baseline sm:gap-8"
            >
              <span className="font-mono text-sm text-muted">{stage.n}</span>
              <div className="max-w-xl">
                <h3 className="font-display text-xl font-medium text-ink">
                  {stage.title}
                </h3>
                <p className="mt-2 text-[0.95rem] leading-relaxed text-muted">
                  {stage.body}
                </p>
              </div>
              {stage.source && (
                <span className="justify-self-start rounded-full bg-accent-soft px-3 py-1 font-mono text-[11px] uppercase tracking-[0.1em] text-accent sm:justify-self-end">
                  {stage.source}
                </span>
              )}
            </li>
          ))}
        </ol>
      </div>
    </section>
  )
}
