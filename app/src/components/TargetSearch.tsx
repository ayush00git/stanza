import { useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import type { Complex, SearchSource } from '../lib/api'
import { searchComplexes, checkHealth } from '../lib/api'
import ComplexCard from './ComplexCard'

type Status = 'idle' | 'searching' | 'done' | 'error'

const examples = ['TP53', 'Aurora kinase', 'EGFR', 'beta-lactamase']

// Rank by dimer confidence — highest-confidence structures first.
function rank(a: Complex, b: Complex) {
  return b.dimer_plddt_avg - a.dimer_plddt_avg
}

export default function TargetSearch() {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<Complex[]>([])
  const [status, setStatus] = useState<Status>('idle')
  const [source, setSource] = useState<SearchSource | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [online, setOnline] = useState<boolean | null>(null)

  const cancelRef = useRef<(() => void) | null>(null)

  // Probe the backend once so we can tell the user if it's down.
  useEffect(() => {
    const ctrl = new AbortController()
    checkHealth(ctrl.signal).then(setOnline)
    return () => {
      ctrl.abort()
      cancelRef.current?.()
    }
  }, [])

  function run(q: string) {
    const trimmed = q.trim()
    if (!trimmed) return
    cancelRef.current?.()

    setResults([])
    setError(null)
    setSource(null)
    setStatus('searching')

    const seen = new Set<string>()
    cancelRef.current = searchComplexes(trimmed, {
      onResult: (complex) => {
        if (seen.has(complex.uniprot_id)) return
        seen.add(complex.uniprot_id)
        setResults((prev) => [...prev, complex].sort(rank))
      },
      onDone: (src) => {
        setSource(src)
        setStatus('done')
      },
      onError: (message) => {
        setError(message)
        setStatus('error')
      },
    })
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    run(query)
  }

  const showEmpty = status === 'done' && results.length === 0

  return (
    <section id="search" className="border-t border-hairline">
      <div className="mx-auto max-w-5xl px-6 py-20 sm:py-24">
        <div className="max-w-xl">
          <p className="font-mono text-xs uppercase tracking-[0.2em] text-accent">
            Live search
          </p>
          <h2 className="mt-4 font-display text-3xl font-normal leading-tight tracking-[-0.01em] text-ink sm:text-4xl">
            Search a target. Structures stream in as we find them.
          </h2>
          <p className="mt-4 text-[0.95rem] leading-relaxed text-muted">
            Query UniProt by gene or protein name. Each hit is enriched with its
            AlphaFold monomer and dimer confidence, then ranked by dimer pLDDT.
          </p>
        </div>

        <form onSubmit={onSubmit} className="mt-10 flex flex-col gap-3 sm:flex-row">
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Gene or protein name…"
            aria-label="Search targets by gene or protein name"
            className="w-full rounded-full border border-hairline bg-paper px-5 py-3 text-sm text-ink placeholder:text-muted focus:border-accent focus:outline-none"
          />
          <button
            type="submit"
            disabled={status === 'searching' || !query.trim()}
            className="shrink-0 rounded-full bg-ink px-6 py-3 text-sm font-medium text-paper transition-transform hover:-translate-y-0.5 disabled:opacity-50 disabled:hover:translate-y-0"
          >
            {status === 'searching' ? 'Searching…' : 'Search'}
          </button>
        </form>

        <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2">
          <span className="font-mono text-xs text-muted">Try</span>
          {examples.map((ex) => (
            <button
              key={ex}
              type="button"
              onClick={() => {
                setQuery(ex)
                run(ex)
              }}
              className="rounded-full border border-hairline px-3 py-1 font-mono text-xs text-muted transition-colors hover:border-accent hover:text-accent"
            >
              {ex}
            </button>
          ))}
          {online === false && (
            <span className="font-mono text-xs text-conf-verylow">
              API offline — start the Go server on :8080
            </span>
          )}
        </div>

        {/* Status line */}
        {status !== 'idle' && (
          <p className="mt-8 font-mono text-xs uppercase tracking-[0.15em] text-muted">
            {status === 'searching' && `Streaming… ${results.length} found`}
            {status === 'done' &&
              `${results.length} result${results.length === 1 ? '' : 's'}${
                source === 'fallback' ? ' · fallback' : ''
              }`}
            {status === 'error' && (
              <span className="text-conf-verylow">{error}</span>
            )}
          </p>
        )}

        {results.length > 0 && (
          <div className="mt-6 grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
            {results.map((complex) => (
              <ComplexCard key={complex.uniprot_id} complex={complex} />
            ))}
          </div>
        )}

        {showEmpty && (
          <p className="mt-6 text-sm text-muted">
            No reviewed targets matched “{query}”. Try a gene symbol like TP53.
          </p>
        )}
      </div>
    </section>
  )
}
