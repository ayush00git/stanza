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
 * The stages a generation round moves through, in the order the SSE stream reports
 * them. Unlike RUN_PHASES (a single blind POST), these track a real signal — the
 * stream's `stage` field maps onto this list via genStepIndex — so the list advances
 * with the server rather than on a timer. The 'claude' stage dominates the wall clock.
 */
export const GEN_STEPS = [
  'Reading the mutant pocket',
  'Assembling the design brief',
  'Claude is designing molecules',
  'Collecting the proposals',
  'Screening for drug-likeness',
]

/** Map a generation stream stage onto an index into GEN_STEPS. */
export function genStepIndex(stage: string): number {
  switch (stage) {
    case 'pockets':
      return 0
    case 'prompt':
      return 1
    case 'claude':
      return 2
    case 'proposed':
      return 3
    case 'validate':
    case 'checked':
      return 4
    default:
      return 0
  }
}

/** A completed step's check mark, in Claude's terracotta. */
function StepCheck() {
  return (
    <svg viewBox="0 0 24 24" className="h-4 w-4 flex-none text-claude" fill="none" stroke="currentColor" strokeWidth="2.6">
      <path d="m5 13 4 4L19 7" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

/** The current step's spinner, in Claude's terracotta. */
function StepSpinner() {
  return (
    <svg viewBox="0 0 24 24" className="h-4 w-4 flex-none animate-spin text-claude" fill="none">
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeOpacity="0.25" strokeWidth="3" />
      <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}

type StepsProps = {
  /** Ordered stage labels, revealed one at a time and never erased. */
  phases: string[]
  /**
   * The current step (controlled). Steps before it get a check, the step at it spins.
   * If omitted, the list self-advances on a timer and parks on the last step — for work
   * with no observable progress (a single synchronous POST like Start run).
   */
  activeIndex?: number
  /** Timer cadence when uncontrolled. */
  intervalMs?: number
  className?: string
}

/**
 * Steps — a stacked, sequential progress list in Claude's terracotta. Steps are revealed
 * one at a time and never erased: the ones already passed keep a check, the current one
 * spins. Drive it with a real signal (activeIndex) when there is one, or let it self-advance
 * on a timer when the work is a single opaque call. No step is ever marked done early.
 */
export function Steps({ phases, activeIndex, intervalMs = 2600, className = '' }: StepsProps) {
  const last = phases.length - 1
  const controlled = activeIndex != null
  const [auto, setAuto] = useState(0)

  useEffect(() => {
    if (controlled || auto >= last) return
    const t = setTimeout(() => setAuto((v) => Math.min(v + 1, last)), intervalMs)
    return () => clearTimeout(t)
  }, [auto, last, controlled, intervalMs])

  const current = Math.min(Math.max(controlled ? (activeIndex as number) : auto, 0), last)

  return (
    <ol className={`flex flex-col gap-2 ${className}`} role="status" aria-live="polite">
      {phases.slice(0, current + 1).map((label, i) => {
        const done = i < current
        return (
          <li key={label} className="flex items-center gap-2.5">
            {done ? <StepCheck /> : <StepSpinner />}
            <span className={`text-sm ${done ? 'text-claude-deep/60' : 'font-medium text-claude-deep'}`}>
              {label}
            </span>
          </li>
        )
      })}
    </ol>
  )
}
