type VerseProps = {
  /** Each string is one line of verse. */
  lines: string[]
  /** Line number the stanza begins at. Poems are cited by line. */
  startNumber?: number
  /** Stagger the entrance of each line on mount. */
  animate?: boolean
  className?: string
}

/**
 * A stanza set like an annotated anthology: mono line numbers in a hairline
 * gutter, the verse itself in the display face. This is the signature block
 * of the site and is reused wherever verse appears.
 */
export default function Verse({
  lines,
  startNumber = 1,
  animate = false,
  className = '',
}: VerseProps) {
  return (
    <div className={`border-l border-hairline ${className}`}>
      {lines.map((line, i) => (
        <div
          key={i}
          className={`grid grid-cols-[2.5rem_1fr] items-baseline gap-x-4 ${
            animate ? 'rise' : ''
          }`}
          style={animate ? { animationDelay: `${0.15 + i * 0.12}s` } : undefined}
        >
          <span
            aria-hidden="true"
            className="select-none pl-4 font-mono text-xs leading-[1.9] text-muted/60 tabular-nums"
          >
            {String(startNumber + i).padStart(2, '0')}
          </span>
          <span className="font-display text-2xl font-light leading-[1.9] tracking-[-0.01em] text-ink sm:text-[1.7rem]">
            {line}
          </span>
        </div>
      ))}
    </div>
  )
}
