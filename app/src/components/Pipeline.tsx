/** Which structural track a stage operates on. This is the spine of the product:
 *  every structure, pocket, and dock exists as a wild-type copy and a mutant copy,
 *  and a stage is defined by which of the two it touches. */
type Track = 'wt' | 'mutant' | 'both' | 'none'

type Stage = {
  n: string
  title: string
  body: string
  track: Track
  tool?: string
}

const stages: Stage[] = [
  {
    n: '01',
    title: 'Target and mutation',
    body: 'A run opens with an accession and a substitution — P01116, G12C. The mutation is an input, not an annotation: it decides every pocket and every dock downstream.',
    track: 'none',
    tool: 'UniProt',
  },
  {
    n: '02',
    title: 'Structure acquisition',
    body: 'Prefer an experimental co-crystal when one exists, since a holo pocket is already open around a ligand. Fall back to a predicted model, carrying per-residue pLDDT forward.',
    track: 'wt',
    tool: 'PDB · AlphaFold',
  },
  {
    n: '03',
    title: 'Mutagenesis',
    body: 'Swap the side chain on the wild-type structure to build the resistant one. Now there are two structures that differ by a single residue — and that residue is the whole problem.',
    track: 'mutant',
    tool: 'PDBFixer',
  },
  {
    n: '04',
    title: 'Pocket delta',
    body: 'Detect pockets on both structures and diff them. What the mutation changed — volume, key residues, the new nucleophile — becomes the context the generator designs against.',
    track: 'both',
    tool: 'fpocket',
  },
  {
    n: '05',
    title: 'Molecule generation',
    body: 'Claude proposes novel SMILES conditioned on the mutant pocket, the delta, and how the last round scored. Structured tool output, so the loop parses chemistry rather than prose.',
    track: 'mutant',
    tool: 'Claude Opus 4.8',
  },
  {
    n: '06',
    title: 'Validation',
    body: 'Parse every proposal, drop the invalid and the duplicated, and keep drug-likeness honest with QED, Lipinski, and synthetic accessibility before anything expensive runs.',
    track: 'none',
    tool: 'RDKit',
  },
  {
    n: '07',
    title: 'Dual-track docking',
    body: 'Dock each survivor into both pockets from a shared box, so any difference in affinity comes from the receptor rather than the search. Cache by molecule and pocket; never dock twice.',
    track: 'both',
    tool: 'AutoDock Vina',
  },
  {
    n: '08',
    title: 'Selectivity ranking',
    body: 'Score the pool on mutant potency, selectivity margin, and drug-likeness, then feed the best and the worst back to the generator for the next round.',
    track: 'both',
  },
]

const trackLabel: Record<Track, string> = {
  wt: 'WT',
  mutant: 'MUT',
  both: 'WT + MUT',
  none: '—',
}

const trackStyle: Record<Track, string> = {
  wt: 'border-hairline text-muted',
  mutant: 'border-accent/30 bg-accent-soft text-accent',
  both: 'border-[var(--color-gain)]/25 bg-[var(--color-gain-soft)] text-[var(--color-gain)]',
  none: 'border-hairline text-muted/60',
}

export default function Pipeline() {
  return (
    <section id="pipeline" className="border-t border-hairline bg-paper-deep/50">
      <div className="mx-auto max-w-5xl px-6 py-20 sm:py-24">
        <div className="max-w-xl">
          <p className="font-mono text-xs uppercase tracking-[0.2em] text-accent">
            The pipeline
          </p>
          <h2 className="mt-4 font-display text-3xl font-normal leading-tight tracking-[-0.01em] text-ink sm:text-4xl">
            Eight stages, two tracks, one loop.
          </h2>
          <p className="mt-5 text-[0.95rem] leading-relaxed text-muted">
            Stages five through eight repeat. Each round sees the scores of the
            last one, so the chemistry sharpens against the mutant pocket until
            the budget runs out or the pool stops improving.
          </p>
        </div>

        <ol className="mt-14 border-t border-hairline">
          {stages.map((stage) => (
            <li
              key={stage.n}
              className="grid gap-4 border-b border-hairline py-6 sm:grid-cols-[3rem_1fr_auto] sm:items-baseline sm:gap-8"
            >
              <span className="font-mono text-sm text-muted">{stage.n}</span>

              <div className="max-w-xl">
                <h3 className="font-display text-xl font-medium text-ink">
                  {stage.title}
                </h3>
                <p className="mt-2 text-[0.95rem] leading-relaxed text-muted">
                  {stage.body}
                </p>
                {stage.tool && (
                  <p className="mt-3 font-mono text-[11px] uppercase tracking-[0.1em] text-muted/80">
                    {stage.tool}
                  </p>
                )}
              </div>

              <span
                className={`justify-self-start whitespace-nowrap rounded-full border px-3 py-1 font-mono text-[11px] uppercase tracking-[0.1em] sm:justify-self-end ${trackStyle[stage.track]}`}
                title={
                  stage.track === 'none'
                    ? 'Runs once, independent of track'
                    : `Runs on the ${trackLabel[stage.track]} track`
                }
              >
                {trackLabel[stage.track]}
              </span>
            </li>
          ))}
        </ol>
      </div>
    </section>
  )
}
