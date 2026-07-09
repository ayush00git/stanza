import { isCovalentCredited, type CovalentDock } from '../../lib/api'

/** Human label for a warhead class, e.g. "vinyl_sulfonamide" → "vinyl sulfonamide". */
function warheadLabel(type: string | undefined): string {
  return (type ?? 'warhead').replace(/_/g, ' ')
}

/** "3.75 Å (median of 5 seeds, spread 0.43 Å)" — reach without its spread is a fiction. */
function reachPhrase(c: CovalentDock): string {
  const r = c.reach_distance?.toFixed(2) ?? '?'
  if (!c.replicates || c.replicates < 2) return `${r} Å`
  const sp = c.reach_spread != null ? `, spread ${c.reach_spread.toFixed(2)} Å` : ''
  return `${r} Å (median of ${c.replicates} seeds${sp})`
}

/** A full-sentence explanation of the covalent model, used as a tooltip. */
export function covalentTitle(c: CovalentDock): string {
  const warhead = warheadLabel(c.warhead_type)
  const raw = `raw mutant ${c.non_covalent_score.toFixed(1)}`
  const shaky = c.uncertain
    ? ' — WARNING: some docking seeds place the warhead in bonding range and others do not, so this credit is decided by the RNG. Treat as indistinguishable, not as a rank.'
    : ''

  switch (c.status) {
    case 'tethered':
    case 'in_reach': {
      const parts = [
        `Covalent tether to ${c.target_residue}`,
        `${warhead} warhead reaches the thiol at ${reachPhrase(c)}`,
        `+${c.credit.toFixed(2)} kcal/mol bond credit (${raw})`,
      ]
      if (c.status === 'tethered' && c.bond_distance) {
        parts.push(`tethered S–C ${c.bond_distance.toFixed(2)} Å`)
      } else {
        parts.push('no valid tethered pose was built, so the docked pose is shown')
      }
      return parts.join(' · ') + shaky
    }
    case 'out_of_reach':
      return [
        `${warhead} warhead, but it cannot reach ${c.target_residue}`,
        `closest approach ${reachPhrase(c)}`,
        `no covalent credit applied (${raw})`,
      ].join(' · ') + shaky
    case 'unreadable_pose':
      return `${warhead} warhead, but the docked pose could not be mapped onto the ligand, so its reach to ${c.target_residue} is unknown — this is a failed measurement, not a negative result${c.note ? ` (${c.note})` : ''}`
    case 'assess_failed':
      return `Covalent assessment failed${c.note ? `: ${c.note}` : ''} — the mutant score is non-covalent`
    case 'no_thiol':
      return `${c.target_residue} carries no thiol, so a ${warhead} warhead cannot bond it`
  }
}

/**
 * A compact pill describing what the covalent model concluded for this molecule.
 *
 * A credited binder tethers to the mutated cysteine, which the wild type (no thiol)
 * cannot do — that is real selectivity rather than the docking noise a non-covalent
 * score shows. But a warhead that never reaches the thiol, and a warhead whose reach
 * could not be measured, must not wear the same badge: reporting them as "covalent"
 * is how a broken measurement passes for a result.
 */
export default function CovalentBadge({
  covalent,
  className = '',
}: {
  covalent: CovalentDock
  className?: string
}) {
  const credited = isCovalentCredited(covalent)
  const failed = covalent.status === 'unreadable_pose' || covalent.status === 'assess_failed'
  // A credit that flips with the seed must not look as confident as one that holds.
  const shaky = covalent.uncertain === true

  const tone = shaky
    ? 'bg-paper-deep text-muted ring-1 ring-inset ring-conf-verylow/40'
    : credited
      ? 'bg-accent-soft text-accent'
      : failed
        ? 'bg-paper-deep text-conf-verylow'
        : 'bg-paper-deep text-muted'

  const label = shaky ? 'seed-dependent' : credited ? 'covalent' : failed ? 'not assessed' : 'no reach'

  return (
    <span
      title={covalentTitle(covalent)}
      className={`inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-[11px] font-medium leading-none ${tone} ${className}`}
    >
      {/* hexagon glyph reads as a covalent bond */}
      <span aria-hidden="true">⬡</span>
      <span>{label}</span>
      {covalent.warhead_type && (
        <span className={credited && !shaky ? 'text-accent/70' : 'opacity-70'}>
          · {warheadLabel(covalent.warhead_type)}
        </span>
      )}
    </span>
  )
}
