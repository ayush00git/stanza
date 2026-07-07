/** AlphaFold pLDDT confidence bands, official colour scale. */
const bands = [
  { label: 'Very high', min: 90, color: 'var(--color-conf-veryhigh)' },
  { label: 'Confident', min: 70, color: 'var(--color-conf-confident)' },
  { label: 'Low', min: 50, color: 'var(--color-conf-low)' },
  { label: 'Very low', min: 0, color: 'var(--color-conf-verylow)' },
]

function bandFor(plddt: number) {
  return bands.find((b) => plddt >= b.min) ?? bands[bands.length - 1]
}

type SequenceProps = {
  /** Single-letter amino-acid codes. */
  residues: string
  /** Per-residue pLDDT confidence, 0–100, aligned to `residues`. */
  confidence: number[]
  /** Residue number the fragment starts at. */
  startResidue?: number
  showLegend?: boolean
  className?: string
}

/**
 * The signature block: a protein fragment set like an annotated structure
 * record — single-letter residues in mono, each over a pLDDT confidence bar,
 * with residue positions ticked every ten. Residue numbering is meaningful
 * here (targets are cited by residue range).
 */
export default function Sequence({
  residues,
  confidence,
  startResidue = 1,
  showLegend = true,
  className = '',
}: SequenceProps) {
  const letters = residues.split('')

  return (
    <div className={className}>
      <div className="flex flex-wrap gap-y-3">
        {letters.map((aa, i) => {
          const pos = startResidue + i
          const tick = pos % 10 === 0
          return (
            <div key={i} className="flex w-[1.15rem] flex-col items-center">
              <span className="font-mono text-sm leading-none text-ink">
                {aa}
              </span>
              <span
                className="mt-1.5 h-1 w-3.5 rounded-full"
                style={{ backgroundColor: bandFor(confidence[i] ?? 0).color }}
              />
              <span className="mt-1 h-3 font-mono text-[10px] leading-none text-muted tabular-nums">
                {tick ? pos : ''}
              </span>
            </div>
          )
        })}
      </div>

      {showLegend && (
        <div className="mt-6 flex flex-wrap gap-x-5 gap-y-2 border-t border-hairline pt-4">
          <span className="font-mono text-[11px] uppercase tracking-[0.15em] text-muted">
            pLDDT
          </span>
          {bands.map((band) => (
            <span
              key={band.label}
              className="flex items-center gap-1.5 text-[11px] text-muted"
            >
              <span
                className="h-2 w-2 rounded-full"
                style={{ backgroundColor: band.color }}
              />
              {band.label}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
