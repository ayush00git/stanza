import { useEffect, useState } from 'react'

type ThinkingProps = {
  /** Ordered labels. The last one holds once reached, rather than looping. */
  phases: string[]
  /** How long each label holds before advancing. */
  intervalMs?: number
  /** Show seconds elapsed, so a slow run never looks like a hung one. */
  showElapsed?: boolean
  className?: string
}

/**
 * A waiting indicator for work whose real progress we cannot observe: the run
 * is built inside one synchronous POST, so there is nothing to poll. The labels
 * advance in the order the server does the work and hold on the final phase —
 * never looping back, which would read as a stall, and never racing ahead to a
 * phase that has certainly not finished.
 */
export default function Thinking({
  phases,
  intervalMs = 2600,
  showElapsed = true,
  className = '',
}: ThinkingProps) {
  const [i, setI] = useState(0)
  const [elapsed, setElapsed] = useState(0)

  const last = phases.length - 1

  useEffect(() => {
    if (i >= last) return
    const t = setTimeout(() => setI((v) => Math.min(v + 1, last)), intervalMs)
    return () => clearTimeout(t)
  }, [i, last, intervalMs])

  useEffect(() => {
    if (!showElapsed) return
    const t = setInterval(() => setElapsed((s) => s + 1), 1000)
    return () => clearInterval(t)
  }, [showElapsed])

  return (
    <div
      className={`flex items-baseline gap-2 ${className}`}
      role="status"
      aria-live="polite"
    >
      {/* Only the current phase is announced; the dots are decoration. */}
      <span className="shimmer text-sm font-medium">{phases[i]}</span>

      <span aria-hidden className="flex items-baseline gap-[3px] pb-0.5">
        {[0, 1, 2].map((d) => (
          <span
            key={d}
            className="blink h-[3px] w-[3px] rounded-full bg-[var(--color-claude)]"
            style={{ animationDelay: `${d * 0.18}s` }}
          />
        ))}
      </span>

      {showElapsed && elapsed > 2 && (
        <span className="font-mono text-xs tabular-nums text-muted/70">
          {elapsed}s
        </span>
      )}
    </div>
  )
}

/** The phases POST /runs actually moves through, in order. */
export const RUN_PHASES = [
  'Reading the mutation',
  'Resolving the accession',
  'Hunting a co-crystal',
  'Pulling the structure',
  'Swapping the side chain',
  'Pairing the two tracks',
]

/**
 * The phases POST /runs/:id/generate moves through: snapshot the pocket context
 * and the scored history, pull the curated site's prior art, ask Claude, then
 * put every proposal through the RDKit gate. The Claude call dominates the wall
 * clock, which is why the middle of this list is where it lingers.
 */
export const GEN_PHASES = [
  'Reading the mutant pocket',
  'Recalling the prior art',
  'Weighing the last round',
  'Sketching scaffolds',
  'Placing the warhead',
  'Canonicalising the SMILES',
  'Culling the duplicates',
  'Checking drug-likeness',
]
