type Track = {
  label: string
  score: number
  tone: 'wt' | 'mutant'
}

type DualTrackProps = {
  wt: number
  mutant: number
  /** Widest bar is drawn against this affinity, in kcal/mol. */
  floor?: number
  className?: string
}

const toneStyles: Record<Track['tone'], { bar: string; text: string }> = {
  wt: { bar: 'bg-muted/45', text: 'text-muted' },
  mutant: { bar: 'bg-accent', text: 'text-ink' },
}

/**
 * The signature block: one molecule's paired Vina affinities, drawn as bars
 * against a shared axis. Vina scores are negative kcal/mol, so a longer bar is
 * tighter binding — the mutant track should outrun the wild-type track, and the
 * gap between the two bar ends *is* the selectivity margin. Everything the
 * pipeline does is in service of opening that gap.
 */
export default function DualTrack({
  wt,
  mutant,
  floor = -12,
  className = '',
}: DualTrackProps) {
  const margin = wt - mutant
  const pct = (score: number) => Math.min(100, (score / floor) * 100)

  const tracks: Track[] = [
    { label: 'Mutant', score: mutant, tone: 'mutant' },
    { label: 'Wild type', score: wt, tone: 'wt' },
  ]

  return (
    <div className={className}>
      <div className="space-y-3">
        {tracks.map((track, i) => (
          <div key={track.label}>
            <div className="flex items-baseline justify-between">
              <span
                className={`font-mono text-[10px] uppercase tracking-[0.15em] ${toneStyles[track.tone].text}`}
              >
                {track.label}
              </span>
              <span
                className={`font-mono text-[0.8rem] tabular-nums ${toneStyles[track.tone].text}`}
              >
                {track.score.toFixed(1)}
              </span>
            </div>

            <div className="mt-1.5 h-1.5 w-full rounded-full bg-paper-deep">
              <div
                className={`draw h-full rounded-full ${toneStyles[track.tone].bar}`}
                style={{
                  width: `${pct(track.score)}%`,
                  animationDelay: `${0.45 + i * 0.12}s`,
                }}
              />
            </div>
          </div>
        ))}
      </div>

      {/* The gap, named. */}
      <div className="mt-4 flex items-center justify-between rounded-lg bg-[var(--color-gain-soft)] px-3.5 py-2.5">
        <span className="font-mono text-[10px] uppercase tracking-[0.15em] text-[var(--color-gain)]">
          Selectivity margin
        </span>
        <span className="font-mono text-[0.8rem] tabular-nums text-[var(--color-gain)]">
          +{margin.toFixed(1)} kcal/mol
        </span>
      </div>

      <p className="mt-2.5 text-[0.75rem] leading-relaxed text-muted">
        <span className="font-mono text-[10px] text-muted">
          wt_score − mutant_score
        </span>{' '}
        — binds the mutant, spares the wild type.
      </p>
    </div>
  )
}
