import type { CovalentDock } from '../../lib/api'

/** Human label for a warhead class, e.g. "vinyl_sulfonamide" → "vinyl sulfonamide". */
function warheadLabel(type: string): string {
  return type.replace(/_/g, ' ')
}

/** A full-sentence explanation of the covalent model, used as a tooltip. */
export function covalentTitle(c: CovalentDock): string {
  const parts = [
    `Covalent tether to ${c.target_residue}`,
    `${warheadLabel(c.warhead_type)} warhead reaches the thiol at ${c.reach_distance.toFixed(2)} Å`,
    `+${c.credit.toFixed(2)} kcal/mol bond credit (raw mutant ${c.non_covalent_score.toFixed(1)})`,
  ]
  if (c.bond_distance) parts.push(`tethered S–C ${c.bond_distance.toFixed(2)} Å`)
  return parts.join(' · ')
}

/**
 * A compact pill marking a molecule that binds the mutant covalently — the warhead
 * tethers to the mutated cysteine, which the wild type (no thiol) cannot do, so the
 * selectivity is real rather than the docking noise a non-covalent score shows.
 */
export default function CovalentBadge({ covalent, className = '' }: { covalent: CovalentDock; className?: string }) {
  return (
    <span
      title={covalentTitle(covalent)}
      className={`inline-flex items-center gap-1 rounded-sm bg-accent-soft px-1.5 py-0.5 text-[11px] font-medium leading-none text-accent ${className}`}
    >
      {/* hexagon glyph reads as a covalent bond */}
      <span aria-hidden="true">⬡</span>
      <span>covalent</span>
      <span className="text-accent/70">· {warheadLabel(covalent.warhead_type)}</span>
    </span>
  )
}
