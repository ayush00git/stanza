const warheads = [
  { name: 'Acrylamide', mech: 'Michael' },
  { name: 'Vinyl sulfonamide', mech: 'Michael' },
  { name: 'Cyanoacrylamide', mech: 'Michael' },
  { name: 'Propiolamide', mech: 'Michael (yne)' },
  { name: 'Haloacetamide', mech: 'SN2' },
]

const REACH_IDEAL = 3.5
const REACH_MAX = 5.0
const AXIS_MIN = 3.0
const AXIS_MAX = 5.5

const pos = (d: number) => ((d - AXIS_MIN) / (AXIS_MAX - AXIS_MIN)) * 100

/** The geometry gate: reach distance from the warhead's electrophilic carbon to
 *  the cysteine thiol, and the credit it earns. */
function ReachGate() {
  return (
    <div>
      <div className="flex items-baseline justify-between">
        <span className="font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
          Warhead → thiol reach
        </span>
        <span className="font-mono text-[11px] text-muted">Å</span>
      </div>

      {/* Full credit, then a linear ramp, then nothing. */}
      <div className="mt-3 flex h-2 w-full overflow-hidden rounded-full">
        <div
          className="h-full bg-[var(--color-gain)]"
          style={{ width: `${pos(REACH_IDEAL)}%` }}
        />
        <div
          className="h-full bg-gradient-to-r from-[var(--color-gain)] to-paper-deep"
          style={{ width: `${pos(REACH_MAX) - pos(REACH_IDEAL)}%` }}
        />
        <div className="h-full flex-1 bg-paper-deep" />
      </div>

      <div className="relative mt-2 h-4">
        {[AXIS_MIN, REACH_IDEAL, REACH_MAX, AXIS_MAX].map((d) => (
          <span
            key={d}
            className="absolute -translate-x-1/2 font-mono text-[10px] tabular-nums text-muted"
            style={{ left: `${pos(d)}%` }}
          >
            {d.toFixed(1)}
          </span>
        ))}
      </div>

      <dl className="mt-5 space-y-2.5 border-t border-hairline pt-4 text-[0.85rem] text-muted">
        <div className="flex items-baseline justify-between gap-4">
          <dt className="tabular-nums">≤ 3.5 Å — positioned to bond</dt>
          <dd className="whitespace-nowrap tabular-nums text-[var(--color-gain)]">
            Full credit, 4.0 kcal/mol
          </dd>
        </div>
        <div className="flex items-baseline justify-between gap-4">
          <dt className="tabular-nums">3.5 – 5.0 Å — straining</dt>
          <dd className="whitespace-nowrap">Credit decays linearly</dd>
        </div>
        <div className="flex items-baseline justify-between gap-4">
          <dt className="tabular-nums">&gt; 5.0 Å — out of reach</dt>
          <dd className="whitespace-nowrap">No credit</dd>
        </div>
      </dl>
    </div>
  )
}

export default function Covalent() {
  return (
    <section id="covalent" className="border-t border-hairline bg-paper-deep/50">
      <div className="mx-auto max-w-5xl px-6 py-20 sm:py-24">
        <div className="max-w-xl">
          <p className="font-mono text-xs uppercase tracking-[0.2em] text-accent">
            Covalent chemistry
          </p>
          <h2 className="mt-4 font-display text-3xl font-normal leading-tight tracking-[-0.01em] text-ink sm:text-4xl">
            The mutation hands you a handle. Grab it.
          </h2>
        </div>

        <div className="mt-14 grid gap-14 lg:grid-cols-[1.1fr_1fr] lg:items-start">
          <div className="max-w-xl">
            <p className="text-[0.95rem] leading-relaxed text-muted">
              G12C does not just reshape the KRAS pocket — it puts a cysteine
              where a glycine used to be, and a cysteine carries a thiol. A
              molecule with the right electrophile can form a permanent bond to
              that thiol. Wild-type KRAS has no thiol at position 12, so it
              cannot be bonded at all. That is sotorasib&rsquo;s entire
              mechanism, and it is the sharpest selectivity a resistance mutation
              can offer.
            </p>
            <p className="mt-4 text-[0.95rem] leading-relaxed text-muted">
              Docking alone cannot see it. Vina scores non-covalent
              interactions, so it rates a warhead touching a thiol the same as a
              warhead touching thin air, and the WT/mutant difference collapses
              into noise.
            </p>
            <p className="mt-4 text-[0.95rem] leading-relaxed text-muted">
              So Stanza scores the bond separately. It detects the warhead in the
              proposed molecule, measures whether the docked pose actually puts
              that warhead within striking distance of the cysteine, and credits
              the mutant track only when the geometry says a bond could form. The
              wild-type track can never earn the credit. The asymmetry docking
              missed comes back exactly where it belongs.
            </p>

            <p className="mt-8 font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
              Warheads detected
            </p>
            <ul className="mt-4 flex flex-wrap gap-2">
              {warheads.map((w) => (
                <li
                  key={w.name}
                  className="rounded-md border border-hairline bg-paper px-2.5 py-1 font-mono text-[11px] text-muted"
                >
                  {w.name}
                  <span className="ml-2 text-muted/60">{w.mech}</span>
                </li>
              ))}
            </ul>
          </div>

          <div className="rounded-xl border border-hairline bg-paper p-8">
            <ReachGate />

            <p className="mt-6 border-t border-hairline pt-5 text-[0.9rem] leading-relaxed text-muted">
              The credit is a model parameter; the geometry is measured. A
              better-placed warhead scores better, which gives the generation
              loop a gradient to climb.
            </p>
          </div>
        </div>
      </div>
    </section>
  )
}
