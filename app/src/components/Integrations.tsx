type Source = {
  name: string
  role: string
  body: string
  fields: string[]
}

const sources: Source[] = [
  {
    name: 'AlphaFold',
    role: 'Structure',
    body: 'Predicted 3D models for monomers and dimers, with the per-residue confidence that tells you which regions to trust.',
    fields: ['pLDDT', 'PAE', 'Monomer / dimer'],
  },
  {
    name: 'UniProt',
    role: 'Sequence',
    body: 'Canonical sequences, isoforms, domains, and functional annotations — the reference layer every target is built on.',
    fields: ['Sequence', 'Domains', 'Annotations'],
  },
]

export default function Integrations() {
  return (
    <section id="data" className="mx-auto max-w-5xl px-6 py-20 sm:py-24">
      <div className="flex items-baseline justify-between border-b border-hairline pb-5">
        <h2 className="font-display text-3xl font-normal tracking-[-0.01em] text-ink sm:text-4xl">
          Built on trusted data
        </h2>
        <span className="font-mono text-xs uppercase tracking-[0.2em] text-muted">
          Live integrations
        </span>
      </div>

      <div className="mt-12 grid gap-6 sm:grid-cols-2">
        {sources.map((source) => (
          <article
            key={source.name}
            className="rounded-xl border border-hairline bg-paper p-8"
          >
            <div className="flex items-baseline justify-between">
              <h3 className="font-display text-2xl font-medium text-ink">
                {source.name}
              </h3>
              <span className="font-mono text-[11px] uppercase tracking-[0.15em] text-accent">
                {source.role}
              </span>
            </div>
            <p className="mt-4 text-[0.95rem] leading-relaxed text-muted">
              {source.body}
            </p>
            <ul className="mt-6 flex flex-wrap gap-2">
              {source.fields.map((field) => (
                <li
                  key={field}
                  className="rounded-md border border-hairline px-2.5 py-1 font-mono text-[11px] text-muted"
                >
                  {field}
                </li>
              ))}
            </ul>
          </article>
        ))}
      </div>
    </section>
  )
}
