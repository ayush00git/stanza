/** AlphaFold pLDDT confidence bands, official colour scale. */
export type Band = {
  label: string
  min: number
  color: string
}

export const plddtBands: Band[] = [
  { label: 'Very high', min: 90, color: 'var(--color-conf-veryhigh)' },
  { label: 'Confident', min: 70, color: 'var(--color-conf-confident)' },
  { label: 'Low', min: 50, color: 'var(--color-conf-low)' },
  { label: 'Very low', min: 0, color: 'var(--color-conf-verylow)' },
]

export function plddtBand(plddt: number): Band {
  return plddtBands.find((b) => plddt >= b.min) ?? plddtBands[plddtBands.length - 1]
}
