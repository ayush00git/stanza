import type { Mutation, MutantPocketContext } from '../../lib/api'

type Status = 'loading' | 'done' | 'error'

type Props = {
  status: Status
  context: MutantPocketContext | null
  error: string | null
  mutation: Mutation
}

/** Signed, fixed-precision delta, e.g. "+12.4" / "−3.1" (true minus glyph). */
function signed(x: number, digits = 1): string {
  const s = x.toFixed(digits)
  return x > 0 ? `+${s}` : s.replace('-', '−')
}

/** A labelled metric cell. */
function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="font-mono text-[9px] uppercase tracking-[0.14em] text-muted">{label}</span>
      <span className="font-mono text-sm tabular-nums text-ink">{value}</span>
    </div>
  )
}

/** A small residue chip. `tone` tints gained (accent) vs. lost (muted). */
function Chip({ text, tone = 'plain' }: { text: string; tone?: 'plain' | 'gain' | 'loss' }) {
  const cls =
    tone === 'gain'
      ? 'border-accent/40 bg-accent-soft text-accent'
      : tone === 'loss'
        ? 'border-hairline bg-paper text-muted line-through'
        : 'border-hairline bg-paper text-ink'
  return (
    <span className={`rounded-full border px-2 py-0.5 font-mono text-[10px] ${cls}`}>{text}</span>
  )
}

/**
 * MutationDeltaPanel — the story unique to the resistance product: what the point
 * mutation did to the binding pocket. Renders the mutant pocket's shape/chemistry
 * and the WT→mutant delta (volume/hydrophobicity/polarity shifts, residues
 * gained/lost, and a one-line effect) that the generator designs against.
 */
export default function MutationDeltaPanel({ status, context, error, mutation }: Props) {
  if (status === 'loading') {
    return (
      <p className="animate-pulse font-mono text-xs uppercase tracking-[0.15em] text-muted">
        Analysing the wild-type and mutant pockets…
      </p>
    )
  }
  if (status === 'error') {
    return <p className="font-mono text-sm text-conf-verylow">{error}</p>
  }
  if (!context) {
    return (
      <p className="text-sm text-muted">
        No distinct resistance pocket was resolved for {mutation.raw}. Generation needs a mutant
        pocket to design against.
      </p>
    )
  }

  const mp = context.mutant_pocket
  const d = context.pocket_delta

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      {/* Mutant pocket — the site to bind. */}
      <div className="rounded-lg border border-hairline bg-paper-deep/40 p-4">
        <div className="mb-3 font-mono text-[11px] uppercase tracking-[0.12em] text-ink">
          Mutant pocket
        </div>
        <div className="mb-3 flex flex-wrap gap-1.5">
          {mp.key_residues.length > 0 ? (
            mp.key_residues.map((r) => <Chip key={r} text={r} />)
          ) : (
            <span className="text-xs text-muted">no key residues resolved</span>
          )}
        </div>
        <div className="flex flex-wrap gap-x-8 gap-y-2">
          <Stat label="Volume" value={`${mp.volume.toFixed(0)} Å³`} />
          <Stat label="Hydrophobicity" value={mp.hydrophobicity.toFixed(2)} />
          {mp.polarity != null && <Stat label="Polarity" value={mp.polarity.toFixed(2)} />}
        </div>
      </div>

      {/* Delta — what the mutation changed. */}
      <div className="rounded-lg border border-hairline bg-paper-deep/40 p-4">
        <div className="mb-3 font-mono text-[11px] uppercase tracking-[0.12em] text-ink">
          What changed (WT → mutant)
        </div>

        {d.changed.length > 0 && (
          <div className="mb-3 flex flex-wrap gap-1.5">
            {d.changed.map((c) => (
              <Chip key={c} text={c} />
            ))}
          </div>
        )}

        <div className="mb-3 flex flex-wrap gap-x-8 gap-y-2">
          <Stat label="Δ Volume" value={`${signed(d.d_volume)} Å³`} />
          <Stat label="Δ Hydrophobicity" value={signed(d.d_hydrophobicity, 2)} />
          <Stat label="Δ Polarity" value={signed(d.d_polarity, 2)} />
        </div>

        {(d.residues_gained?.length || d.residues_lost?.length) && (
          <div className="mb-3 flex flex-wrap gap-1.5">
            {d.residues_gained?.map((r) => <Chip key={`g-${r}`} text={`+${r}`} tone="gain" />)}
            {d.residues_lost?.map((r) => <Chip key={`l-${r}`} text={r} tone="loss" />)}
          </div>
        )}

        {d.effect && <p className="text-xs leading-relaxed text-muted">{d.effect}</p>}
      </div>
    </div>
  )
}
