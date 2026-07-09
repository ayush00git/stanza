import { isCovalentFeasible, type CovalentDock } from '../../lib/api'

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

/** "attack angle 104°" — the approach at the electrophilic carbon; omitted if unmeasured. */
function anglePhrase(c: CovalentDock): string | null {
  return c.attack_angle != null ? `attack angle ${Math.round(c.attack_angle)}°` : null
}

/** "geometry from Vina mode 2 (−7.3 kcal/mol)" — which bound pose the reach was read off. */
function modePhrase(c: CovalentDock): string | null {
  if (!c.mode_rank) return null
  // This kcal/mol is the receptor's own Vina affinity for the pose, not a covalent
  // energy — the covalent term is kinetic and has no ΔG, so it is never printed here.
  const aff = c.mode_affinity != null ? ` (${c.mode_affinity.toFixed(1)} kcal/mol)` : ''
  return `geometry from Vina mode ${c.mode_rank}${aff}`
}

/** A full-sentence explanation of the covalent geometry verdict, used as a tooltip. */
export function covalentTitle(c: CovalentDock): string {
  const warhead = warheadLabel(c.warhead_type)
  const shaky = c.uncertain
    ? ' — WARNING: some docking seeds place the warhead where it can attack and others do not, so this call is decided by the RNG. Treat as indistinguishable, not as a rank.'
    : ''

  switch (c.status) {
    case 'tethered':
    case 'feasible': {
      const parts = [
        `Covalent feasibility ${c.feasibility.toFixed(2)} — ${warhead} warhead can attack ${c.target_residue}`,
        `reach ${reachPhrase(c)}`,
      ]
      const angle = anglePhrase(c)
      if (angle) parts.push(angle)
      const mode = modePhrase(c)
      if (mode) parts.push(mode)
      if (c.status === 'tethered' && c.bond_distance) {
        parts.push(`tethered S–C ${c.bond_distance.toFixed(2)} Å`)
      } else if (c.status === 'feasible') {
        parts.push('no valid adduct pose was built, so the docked pose is shown')
      }
      return parts.join(' · ') + shaky
    }
    case 'infeasible': {
      // Report both the reach ("too far") and the angle ("wrong attack angle") so the
      // reader can see which one killed the bond.
      const parts = [`${warhead} warhead cannot attack ${c.target_residue}`]
      if (c.reach_distance != null) parts.push(`closest approach ${reachPhrase(c)}`)
      const angle = anglePhrase(c)
      if (angle) parts.push(`${angle} off the ideal trajectory`)
      const mode = modePhrase(c)
      if (mode) parts.push(mode)
      return parts.join(' · ') + shaky
    }
    case 'unreadable_pose':
      return `${warhead} warhead, but the docked pose could not be mapped onto the ligand, so its reach to ${c.target_residue} is unknown — a failed measurement, not a negative result${c.note ? ` (${c.note})` : ''}`
    case 'assess_failed':
      return `Covalent assessment failed${c.note ? `: ${c.note}` : ''} — the warhead's reach to the thiol could not be checked`
    case 'no_thiol':
      return `${c.target_residue} carries no thiol, so a ${warhead} warhead cannot bond it`
  }
}

/**
 * A compact pill describing the covalent geometry verdict for this molecule.
 *
 * A feasible warhead can attack the mutated cysteine's thiol, which the wild type
 * (Gly12, no thiol) cannot offer at all — that is where a covalent inhibitor's
 * selectivity comes from, and non-covalent docking is blind to it. But a warhead that
 * cannot reach or is at the wrong angle, and a warhead whose geometry could not be
 * read, must not wear the same badge: showing them as "covalent" is how a broken
 * measurement passes for a result. The pill reports feasibility (0–1), never an energy.
 */
export default function CovalentBadge({
  covalent,
  className = '',
}: {
  covalent: CovalentDock
  className?: string
}) {
  const feasible = isCovalentFeasible(covalent)
  const failed = covalent.status === 'unreadable_pose' || covalent.status === 'assess_failed'
  // A call that flips with the seed is a coin flip — it must not look as confident as
  // one that holds, so seed-dependent styling wins over the feasible accent pill.
  const shaky = covalent.uncertain === true

  const tone = shaky
    ? 'bg-paper-deep text-muted ring-1 ring-inset ring-conf-verylow/40'
    : feasible
      ? 'bg-accent-soft text-accent'
      : failed
        ? 'bg-paper-deep text-conf-verylow'
        : 'bg-paper-deep text-muted'

  // "no attack" (not "no reach"): an infeasible warhead may be in range but approaching
  // the electrophilic carbon at an impossible angle.
  const label = shaky
    ? 'seed-dependent'
    : feasible
      ? 'covalent'
      : failed
        ? 'not assessed'
        : covalent.status === 'infeasible'
          ? 'no attack'
          : 'no thiol'

  const accent = feasible && !shaky

  return (
    <span
      title={covalentTitle(covalent)}
      className={`inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs font-medium leading-none ${tone} ${className}`}
    >
      {/* hexagon glyph reads as a covalent bond */}
      <span aria-hidden="true">⬡</span>
      <span>{label}</span>
      {covalent.warhead_type && (
        <span className={accent ? 'text-accent/70' : 'opacity-70'}>
          · {warheadLabel(covalent.warhead_type)}
        </span>
      )}
      {accent && (
        <span className="tabular-nums text-accent/70">· {covalent.feasibility.toFixed(2)}</span>
      )}
    </span>
  )
}
