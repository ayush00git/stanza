type Source = {
  name: string
  role: string
  body: string
  fields: string[]
}

const sources: Source[] = [
  {
    name: 'Claude',
    role: 'Generation',
    body: 'Opus 4.8 proposes the chemistry, conditioned on the mutant pocket, the WT→mutant delta, and how the previous round scored. Tool-structured output, so the loop reads SMILES rather than prose.',
    fields: ['Opus 4.8', 'Tool use', 'Round feedback'],
  },
  {
    name: 'AlphaFold',
    role: 'Structure',
    body: 'Predicted models with per-residue pLDDT, used when no experimental structure exists. Confidence travels with the structure, so a pocket built on disordered residues is visibly untrustworthy.',
    fields: ['pLDDT', 'Predicted models'],
  },
  {
    name: 'RCSB PDB',
    role: 'Structure',
    body: 'Preferred over prediction whenever a co-crystal exists. A holo pocket is already open around a ligand, which is the conformation a docking box wants to see.',
    fields: ['Co-crystals', 'Holo pockets'],
  },
  {
    name: 'UniProt',
    role: 'Sequence',
    body: 'Canonical sequences, domains, and organism annotations — the reference layer a run resolves its accession and its mutation position against.',
    fields: ['Sequence', 'Domains', 'Taxonomy'],
  },
  {
    name: 'fpocket · Vina',
    role: 'Compute',
    body: 'Pocket detection on both tracks, then docking into both from a shared box. Results are cached by molecule, pocket, and parameters, so a re-proposed molecule never docks twice.',
    fields: ['Pocket detection', 'Dual-track docking', 'Idempotent cache'],
  },
  {
    name: 'RDKit · PDBFixer',
    role: 'Chemistry',
    body: 'PDBFixer builds the mutant side chain. RDKit parses every proposal, rejects the invalid, scores drug-likeness, and finds the warhead before anything expensive runs.',
    fields: ['Mutagenesis', 'QED · Lipinski', 'Warhead SMARTS'],
  },
]

export default function Integrations() {
  return (
    <section id="data" className="mx-auto max-w-5xl px-6 py-20 sm:py-24">
      <div className="flex items-baseline justify-between border-b border-hairline pb-5">
        <h2 className="font-display text-3xl font-normal tracking-[-0.01em] text-ink sm:text-4xl">
          What it&rsquo;s built on
        </h2>
        <span className="font-mono text-xs uppercase tracking-[0.2em] text-muted">
          The stack
        </span>
      </div>

      <div className="mt-12 grid gap-6 sm:grid-cols-2">
        {sources.map((source) => (
          <article
            key={source.name}
            className="rounded-xl border border-hairline bg-paper p-8"
          >
            <div className="flex items-baseline justify-between gap-4">
              <h3 className="font-display text-2xl font-medium text-ink">
                {source.name}
              </h3>
              <span className="whitespace-nowrap font-mono text-[11px] uppercase tracking-[0.15em] text-accent">
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
