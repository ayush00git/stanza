const warheads = [
  { name: 'Acrylamide', mech: 'Michael' },
  { name: 'Vinyl sulfonamide', mech: 'Michael' },
  { name: 'Cyanoacrylamide', mech: 'Michael' },
  { name: 'Propiolamide', mech: 'Michael (yne)' },
  { name: 'Haloacetamide', mech: 'SN2' },
]

// Mirrors the constants in scripts/covalent.py.
const REACH_IDEAL = 3.5 // Bondi S···C van der Waals contact
const REACH_MAX = 4.0
const AXIS_MIN = 3.0
const AXIS_MAX = 4.5

const pos = (d: number) => ((d - AXIS_MIN) / (AXIS_MAX - AXIS_MIN)) * 100

/** Reach: how close the electrophilic carbon gets to the cysteine thiol. */
function ReachGate() {
  return (
    <div>
      <div className="flex items-baseline justify-between">
        <span className="font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
          Reach
        </span>
        <span className="text-[0.8rem] text-muted">
          warhead carbon → thiol sulfur
        </span>
      </div>

      <div className="mt-3 flex h-2 w-full overflow-hidden rounded-full">
        <div
          className="h-full bg-accent"
          style={{ width: `${pos(REACH_IDEAL)}%` }}
        />
        <div
          className="h-full bg-gradient-to-r from-accent to-paper-deep"
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

      <p className="mt-4 text-[0.85rem] leading-relaxed text-muted">
        Full marks at the van der Waals contact distance and closer — a pose that
        overlaps the sulfur cannot do better than touching it. Past{' '}
        <span className="font-mono tabular-nums text-ink">4.0 Å</span> the
        warhead is not covalently competent.
      </p>
    </div>
  )
}

/** Angle: whether the approach trajectory can reach a transition state. */
function AngleGate() {
  return (
    <div>
      <div className="flex items-baseline justify-between">
        <span className="font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
          Attack angle
        </span>
        <span className="text-[0.8rem] text-muted">at the electrophilic carbon</span>
      </div>

      <dl className="mt-4 space-y-2.5 text-[0.85rem] text-muted">
        <div className="flex items-baseline justify-between gap-4">
          <dt>Michael acceptor</dt>
          <dd className="whitespace-nowrap tabular-nums text-ink">
            105° — Bürgi–Dunitz
          </dd>
        </div>
        <div className="flex items-baseline justify-between gap-4">
          <dt>S<sub>N</sub>2 electrophile</dt>
          <dd className="whitespace-nowrap tabular-nums text-ink">
            180° — backside
          </dd>
        </div>
      </dl>

      <p className="mt-4 text-[0.85rem] leading-relaxed text-muted">
        Within 15° of the ideal trajectory the approach is as good as perfect;
        by 40° off it is worthless. Deviation is punished symmetrically, so too
        shallow and too steep cost the same.
      </p>
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
              warhead touching thin air, and the energetic difference between the
              two tracks collapses into noise.
            </p>
            <p className="mt-4 text-[0.95rem] leading-relaxed text-muted">
              So Stanza measures the bond instead of scoring it. It finds the
              warhead, then reads the docked poses for the one that best lines up
              a real attack: close enough to bond, and angled the way that
              chemistry demands. Feasibility is the product of those two — reach
              times angle, zero to one — and it is never invented, only measured.
              The wild-type track has no thiol, so it can never earn any of it.
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
            <p className="font-mono text-[11px] uppercase tracking-[0.15em] text-accent">
              Feasibility = reach × angle
            </p>

            <div className="mt-6 space-y-7">
              <ReachGate />
              <div className="border-t border-hairline pt-7">
                <AngleGate />
              </div>
            </div>

            <div className="mt-7 space-y-3 border-t border-hairline pt-5">
              <p className="text-[0.85rem] leading-relaxed text-muted">
                Only poses the receptor genuinely binds are allowed to
                contribute, so reach cannot be bought with a strained,
                high-energy conformation.
              </p>
              <p className="text-[0.85rem] leading-relaxed text-muted">
                Geometry is read across several docking seeds. When the verdict
                flips between them, the molecule is marked uncertain — shown on
                the board, but scored as zero.
              </p>
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}
