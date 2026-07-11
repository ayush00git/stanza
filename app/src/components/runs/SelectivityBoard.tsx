import type { ReactNode } from 'react'
import { type CovalentDock, type DockPartial, type DockProgress, type Ranking,
  selectivityResolved,
} from '../../lib/api'
import CovalentBadge from './CovalentBadge'

type Props = {
  ranking: Ranking | null
  status: 'idle' | 'loading' | 'done' | 'error'
  error?: string | null
  /** SMILES of the molecule whose poses are currently shown in the viewers. */
  activeSmiles: string | null
  onSelect: (smiles: string) => void
  /** The molecule currently docking, streamed step by step. null when idle. */
  docking?: { smiles: string; progress: DockProgress | null; partial: DockPartial } | null
}

/**
 * The in-flight dock, shown at the foot of the board.
 *
 * A dock is six AutoDock Vina runs plus, for a covalent ligand, a geometry pass over
 * every seed — tens of seconds of CPU that no amount of engineering removes. So the
 * progress line names the step rather than spinning: "mutant pocket docked, seed 2 of 3"
 * is a wait; an unlabelled spinner for a minute is a hang.
 *
 * The statistics appear as they are computed, and never before. A result is streamed the
 * moment it is real — the wild-type affinity lands while the mutant seeds are still
 * running — but the finished LigandDock is only assembled at the end, so a field that is
 * absent here has genuinely not been measured yet. It is not zero, and it is not
 * pending in a buffer somewhere.
 */
function DockingRow({
  smiles,
  progress,
  partial,
}: {
  smiles: string
  progress: DockProgress | null
  partial: DockPartial
}) {
  const pct = progress && progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : null
  const hasResults =
    partial.wt_score != null || partial.mutant_score != null || partial.covalent != null

  return (
    <li aria-live="polite">
      <div className="flex flex-col gap-3 border-b border-hairline bg-paper-deep px-4 py-3.5 last:border-b-0">
        <div className="flex items-start gap-3">
          <span className="mt-0.5 flex h-5 w-5 flex-none items-center justify-center rounded-full bg-accent-soft">
            <span className="h-2 w-2 animate-pulse rounded-full bg-accent" />
          </span>
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <div className="flex flex-wrap items-baseline gap-2">
              <span className="text-xs font-medium text-accent">Docking</span>
              <span className="text-xs text-muted">
                {progress?.message ?? 'preparing'}
                {progress && progress.total > 0 && ` · step ${progress.done} of ${progress.total}`}
              </span>
            </div>
            <p className="break-all font-mono text-xs leading-relaxed text-muted">{smiles}</p>
          </div>
        </div>

        <div className="ml-8 h-0.5 overflow-hidden rounded-full bg-hairline">
          <div
            className="h-full rounded-full bg-accent transition-[width] duration-500"
            style={{ width: `${pct ?? 4}%` }}
          />
        </div>

        {hasResults && (
          <dl className="grid grid-cols-2 gap-x-8 gap-y-3 pl-8 sm:grid-cols-4">
            {partial.wt_score != null && (
              <Stat
                label="Wild-type affinity"
                value={affinity(partial.wt_score)}
                unit="kcal/mol"
                hint="AutoDock Vina affinity against the wild-type pocket. More negative = binds tighter."
              />
            )}
            {partial.mutant_score != null && (
              <Stat
                label="Mutant affinity"
                value={affinity(partial.mutant_score)}
                unit="kcal/mol"
                hint="AutoDock Vina affinity against the mutant pocket — the deepest pose found across the replicate seeds. Raw: no covalent term is folded in."
              />
            )}
            {partial.selectivity != null && (
              <Stat
                label="Selectivity"
                value={signed(partial.selectivity)}
                unit="kcal/mol"
                hint={SELECTIVITY_NOTE}
                tone="text-muted"
              />
            )}
            {partial.covalent && <CovalentStats c={partial.covalent} />}
          </dl>
        )}
      </div>
    </li>
  )
}

/** Round to `digits`, collapsing negative zero so a −0.0001 never prints as "−0.00". */
function round(x: number, digits = 2): number {
  const r = Number(x.toFixed(digits))
  return r === 0 ? 0 : r
}

/** Fixed-decimal string with no negative zero: fitness is a z-score sum near 0. */
function fixed(x: number, digits = 2): string {
  return round(x, digits).toFixed(digits)
}

/**
 * Where a ranked molecule came from. Claude's designs and fetched ChEMBL references share
 * one leaderboard and one ruler; this badge keeps them visually distinct so a reference
 * docked as a control is never mistaken for a generated candidate. Absent on docks recorded
 * before the source was tracked, which render with no badge rather than a guessed one.
 */
function SourceBadge({ source }: { source?: string }) {
  if (source === 'chembl') {
    return (
      <span className="rounded-full border border-hairline bg-paper-deep px-2 py-0.5 text-xs text-muted">
        ChEMBL ref
      </span>
    )
  }
  if (source === 'claude') {
    return (
      <span className="rounded-full bg-claude/10 px-2 py-0.5 text-xs font-medium text-claude-deep">
        Claude
      </span>
    )
  }
  return null
}

/** Signed value with a true minus glyph, e.g. "+4.10" / "−0.30". */
function signed(x: number, digits = 2): string {
  const r = round(x, digits)
  const s = r.toFixed(digits)
  return r > 0 ? `+${s}` : s.replace('-', '−')
}

/** Affinities are negative kcal/mol; render the minus as a glyph, not a hyphen. */
function affinity(x: number): string {
  return fixed(x).replace('-', '−')
}

const SELECTIVITY_NOTE =
  'Selectivity = WT affinity − mutant affinity. For a covalent target it is expected to be ≈0: swapping Gly12 for Cys12 barely changes the pocket shape, so a ligand binds both forms equally well. Real selectivity comes from the covalent bond, which only the mutant can form — see feasibility. Each affinity is the deepest pose found across docking seeds; the seed-to-seed spread is the error bar on this margin.'

/**
 * The caption under Selectivity. Three states, and conflating them is what hid a bug:
 * a margin that beats its own search noise, one that does not (Vina found different
 * basins on different seeds, so the number describes the search rather than the
 * receptor), and one docked before spreads were recorded — unknown, not fine.
 */
function selectivityNote(
  s: { selectivity: number; wt_spread?: number; mutant_spread?: number; replicates?: number },
  covalentRun: boolean,
): string | undefined {
  const resolved = selectivityResolved(s)
  if (resolved === false) {
    const noise = Math.max(s.wt_spread ?? 0, s.mutant_spread ?? 0)
    return `below seed noise (±${noise.toFixed(2)}) — not resolved`
  }
  if (resolved === null) return covalentRun ? 'expected ≈0 here · spread not recorded' : 'spread not recorded'
  return covalentRun ? 'expected ≈0 here' : undefined
}

const FEASIBILITY_NOTE =
  'Covalent feasibility, 0–1 and dimensionless. Can the warhead reach the cysteine thiol and attack it along a viable trajectory? It is a geometry score, NOT an energy: covalent potency is kinetic (kinact/KI) and cannot be expressed in kcal/mol.'

/**
 * One labelled statistic. The label is always spelled out and the unit always shown —
 * a bare "−10.6" beside a bare "0.77" invites the reader to compare a kcal/mol energy
 * with a dimensionless geometry score, which is exactly the confusion that let a
 * hand-picked constant pass for a selectivity prediction.
 */
function Stat({
  label,
  value,
  unit,
  hint,
  tone = 'text-ink',
  note,
}: {
  label: string
  value: ReactNode
  unit?: string
  hint?: string
  tone?: string
  note?: string
}) {
  return (
    <div className="flex flex-col gap-0.5" title={hint}>
      <span className="text-xs uppercase tracking-wide text-muted">{label}</span>
      <span className={`text-sm tabular-nums ${tone}`}>
        {value}
        {unit && <span className="ml-1 text-xs font-normal text-muted">{unit}</span>}
      </span>
      {note && <span className="text-xs leading-tight text-muted/80">{note}</span>}
    </div>
  )
}

/** The covalent statistics, spelled out — including why an infeasible warhead failed. */
function CovalentStats({ c }: { c: CovalentDock }) {
  const measured = c.reach_distance != null && c.attack_angle != null
  const failure =
    c.feasibility <= 0 && measured
      ? c.reach_distance! > 4
        ? 'too far from the thiol'
        : 'wrong attack trajectory'
      : undefined

  return (
    <>
      <Stat
        label="Covalent feasibility"
        value={c.feasibility.toFixed(2)}
        hint={FEASIBILITY_NOTE}
        tone={c.uncertain ? 'text-muted' : c.feasibility > 0 ? 'text-accent' : 'text-muted'}
        note={
          c.uncertain
            ? 'flips with the docking seed — not ranked'
            : failure ?? (c.feasibility > 0 ? `warhead can attack ${c.target_residue}` : undefined)
        }
      />
      {measured && (
        <>
          <Stat
            label="Reach"
            value={c.reach_distance!.toFixed(2)}
            unit="Å"
            hint={`Distance from the warhead's electrophilic carbon to the ${c.target_residue} sulfur, median over ${c.replicates ?? 1} docking seed(s). Full score at ≤3.50 Å (the van der Waals contact distance); zero beyond 4.00 Å.`}
            note={c.reach_spread ? `spread ${c.reach_spread.toFixed(2)} Å` : undefined}
          />
          <Stat
            label="Attack angle"
            value={c.attack_angle!.toFixed(0)}
            unit="°"
            hint="The angle the thiol approaches the electrophilic carbon at. Nucleophilic attack on a Michael acceptor needs ~105° (Bürgi–Dunitz); an SN2 haloacetamide needs ~180°. A warhead at the right distance but the wrong angle cannot react."
          />
        </>
      )}
      {c.bond_distance != null && (
        <Stat
          label="S–C bond"
          value={c.bond_distance.toFixed(2)}
          unit="Å"
          hint="Sulfur–carbon bond length in the tethered covalent adduct built from this pose. A real thioether bond is 1.81 Å."
          note="tethered adduct"
        />
      )}
    </>
  )
}

/**
 * SelectivityBoard — the leaderboard. Ranks docked molecules by composite fitness
 * (Stage 7): mutant potency + selectivity margin + drug-likeness + covalent
 * feasibility, each pool-normalised. Clicking a row loads that molecule's WT + mutant
 * poses into the two viewers.
 *
 * Every statistic is named and carries its unit. The SMILES is never truncated: it is
 * the molecule's identity, and an ellipsis makes two different candidates look alike.
 */
export default function SelectivityBoard({ ranking, status, error, activeSmiles, onSelect, docking }: Props) {
  const rows = ranking?.ranked ?? []
  const covalentRun = rows.some((m) => m.scores.covalent != null)

  // The best selectivity in the pool, not the top-ranked row's — those differ, because
  // fitness ranks on potency, drug-likeness and covalent feasibility too.
  const best = rows.length ? Math.max(...rows.map((m) => m.scores.selectivity)) : null
  // A seed-dependent call is not evidence, so it cannot be the pool's headline number.
  const feasibilities = rows.flatMap((m) =>
    m.scores.covalent && !m.scores.covalent.uncertain ? [m.scores.covalent.feasibility] : [],
  )
  const topFeasibility = feasibilities.length ? Math.max(...feasibilities) : null

  const w = ranking?.weights

  return (
    <section className="flex flex-col">
      <div className="mb-4 flex flex-col gap-1 border-t border-hairline pt-6">
        <h2 className="font-display text-base font-medium text-ink">Selectivity ranking</h2>
        <p className="text-sm text-muted">
          {rows.length === 0
            ? 'Dock candidates to rank them by composite fitness.'
            : `${rows.length} molecule${rows.length !== 1 ? 's' : ''} · click one to load its poses.`}
        </p>
        {covalentRun && (
          <p className="mt-1 text-xs leading-relaxed text-muted">
            This is a covalent target. Selectivity reads ≈0 because both forms of the pocket bind the
            molecule equally — only the mutant offers a thiol to bond. Rank on{' '}
            <span className="text-accent">covalent feasibility</span>.
          </p>
        )}
      </div>

      {status === 'error' ? (
        <p className="text-sm text-conf-verylow">{error ?? 'Ranking failed'}</p>
      ) : rows.length === 0 && !docking ? (
        <div className="rounded-md border border-dashed border-hairline bg-paper px-4 py-8 text-center">
          <p className="text-sm text-muted">
            {status === 'loading' ? 'Ranking…' : 'No docks yet — dock a candidate to rank it here.'}
          </p>
        </div>
      ) : (
        <div className="overflow-hidden rounded-md border border-hairline bg-paper">
          <ul className="flex flex-col">
            {rows.map((m) => {
              const isActive = m.smiles === activeSmiles
              const s = m.scores
              // Claude-designed rows carry a terracotta tint across the whole row (left
              // border + background), so the generated molecules read apart from ChEMBL
              // references at a glance. Non-Claude rows keep the blue active/hover styling.
              const isClaude = s.source === 'claude'
              const rowTone = isClaude
                ? `border-l-2 border-l-claude pl-[14px] ${isActive ? 'bg-claude/15' : 'bg-claude/5 hover:bg-claude/10'}`
                : isActive
                  ? 'border-l-2 border-l-accent bg-accent-soft pl-[14px]'
                  : 'hover:bg-paper-deep'
              const rankTone = isClaude
                ? 'bg-claude text-paper'
                : isActive
                  ? 'bg-accent text-paper'
                  : 'bg-paper-deep text-muted'
              return (
                <li key={m.smiles}>
                  <div
                    role="button"
                    tabIndex={0}
                    onClick={() => onSelect(m.smiles)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault()
                        onSelect(m.smiles)
                      }
                    }}
                    className={`group flex cursor-pointer flex-col gap-3 border-b border-hairline px-4 py-3.5 transition-colors last:border-b-0 ${rowTone}`}
                  >
                    {/* Rank, covalent verdict, and the composite score that produced the rank. */}
                    <div className="flex items-start gap-3">
                      <span
                        className={`mt-0.5 flex h-5 w-5 flex-none items-center justify-center rounded-full text-xs tabular-nums ${rankTone}`}
                      >
                        {m.rank}
                      </span>

                      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                        <div className="flex flex-wrap items-center gap-2">
                          <SourceBadge source={s.source} />
                          {s.covalent && <CovalentBadge covalent={s.covalent} />}
                          {s.fitness != null && (
                            <span
                              className="text-xs text-muted"
                              title="Composite fitness: the pool-normalised weighted sum that produced this rank. Comparable only within this run."
                            >
                              fitness {fixed(s.fitness)}
                            </span>
                          )}
                        </div>
                        {/* Never truncated: the SMILES is the molecule's identity. */}
                        <p className="break-all font-mono text-xs leading-relaxed text-ink select-text">
                          {m.smiles}
                        </p>
                      </div>
                    </div>

                    <dl className="grid grid-cols-2 gap-x-8 gap-y-3 pl-8 sm:grid-cols-4">
                      <Stat
                        label="Wild-type affinity"
                        value={affinity(s.wt_score)}
                        unit="kcal/mol"
                        hint="AutoDock Vina affinity against the wild-type pocket — the deepest pose found across seeds. More negative = binds tighter."
                        note={s.wt_spread ? `spread ${s.wt_spread.toFixed(2)}` : undefined}
                      />
                      <Stat
                        label="Mutant affinity"
                        value={affinity(s.mutant_score)}
                        unit="kcal/mol"
                        hint="AutoDock Vina affinity against the mutant pocket — the deepest pose found across seeds. Raw: no covalent term is folded in."
                        note={s.mutant_spread ? `spread ${s.mutant_spread.toFixed(2)}` : undefined}
                      />
                      <Stat
                        label="Selectivity"
                        value={signed(s.selectivity)}
                        unit="kcal/mol"
                        hint={SELECTIVITY_NOTE}
                        tone={
                          selectivityResolved(s) === false
                            ? 'text-muted'
                            : covalentRun
                              ? 'text-muted'
                              : s.selectivity > 0.3
                                ? 'text-accent'
                                : 'text-ink'
                        }
                        note={selectivityNote(s, covalentRun)}
                      />
                      {s.covalent && <CovalentStats c={s.covalent} />}
                      {s.qed != null && (
                        <Stat
                          label="Drug-likeness"
                          value={s.qed.toFixed(2)}
                          hint="QED, 0–1. Real switch-II inhibitors are large (431–622 Da) and score modestly here; a high QED on a tiny fragment is not an advantage."
                          note="QED, 0–1"
                        />
                      )}
                    </dl>
                  </div>
                </li>
              )
            })}
            {docking && (
              <DockingRow smiles={docking.smiles} progress={docking.progress} partial={docking.partial} />
            )}
          </ul>

          <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t border-hairline bg-paper-deep px-4 py-2.5 text-xs text-muted">
            {/* Name each weight rather than printing a bare ratio: on a covalent run the
                feasibility term carries most of the ranking, and an unlabelled triple that
                silently omits it reads as if selectivity were still doing the work. */}
            <span title="Composite fitness is the weighted sum of these four terms, each normalised across the docked pool.">
              {ranking?.normalization ?? 'zscore'} weights ·{' '}
              {w
                ? `potency ${w.potency} · selectivity ${w.selectivity} · drug-likeness ${w.drug_likeness}` +
                  (w.covalent_feasibility ? ` · feasibility ${w.covalent_feasibility}` : '')
                : '—'}
            </span>
            {/* On a covalent run the headline is feasibility: "best selectivity" would put a
                number that is sampling error in the summary slot. */}
            {covalentRun && topFeasibility != null ? (
              <span title="Highest covalent feasibility in the pool — the warhead most able to attack the thiol. Seed-dependent calls are excluded.">
                Top feasibility <span className="text-accent">{topFeasibility.toFixed(2)}</span>
              </span>
            ) : (
              best != null && (
                <span>
                  Best selectivity{' '}
                  <span className={best > 0.3 ? 'text-accent' : 'text-ink'}>{signed(best)}</span>
                </span>
              )
            )}
          </div>
        </div>
      )}
    </section>
  )
}
