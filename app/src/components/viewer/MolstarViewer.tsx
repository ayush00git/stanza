import { useMolstar } from './useMolstar'

interface MolstarViewerProps {
  url?: string
  label: string
  plddt?: number
  representation: string
}

/**
 * A single Mol* canvas: renders one structure loaded from a remote URL, with a
 * caption and loading/error/empty overlays. All Mol* logic lives in useMolstar.
 */
export default function MolstarViewer({ url, label, plddt, representation }: MolstarViewerProps) {
  const { containerRef, isLoading, error } = useMolstar({
    structureUrl: url,
    representation,
    label,
  })

  return (
    <div className="flex flex-1 flex-col">
      <div className="flex items-baseline justify-between border-b border-hairline px-3 py-2">
        <span className="font-mono text-[11px] uppercase tracking-[0.12em] text-ink">{label}</span>
        {plddt !== undefined && plddt > 0 && (
          <span className="font-mono text-[11px] text-muted">pLDDT {plddt.toFixed(1)}</span>
        )}
      </div>

      <div className="relative min-h-0 flex-1">
        {/* Mol* mounts its canvas into this element. */}
        <div ref={containerRef} className="absolute inset-0" />

        {!url && (
          <div className="absolute inset-0 flex items-center justify-center bg-paper-deep">
            <span className="font-mono text-xs text-muted">No structure available</span>
          </div>
        )}
        {url && isLoading && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-paper-deep/70">
            <span className="animate-pulse font-mono text-xs uppercase tracking-[0.15em] text-muted">
              Loading structure…
            </span>
          </div>
        )}
        {error && (
          <div className="absolute inset-0 flex items-center justify-center bg-paper-deep p-6 text-center">
            <span className="font-mono text-xs text-conf-verylow">{error}</span>
          </div>
        )}
      </div>
    </div>
  )
}
