import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import type { Complex, SearchSource } from './api'
import { searchComplexes, checkHealth } from './api'

type Status = 'idle' | 'searching' | 'done' | 'error'

interface SearchState {
  query: string
  setQuery: (q: string) => void
  results: Complex[]
  status: Status
  source: SearchSource | null
  error: string | null
  online: boolean | null
  run: (q: string) => void
}

const SearchContext = createContext<SearchState | null>(null)

// Rank by dimer confidence — highest-confidence structures first.
function rank(a: Complex, b: Complex) {
  return b.dimer_plddt_avg - a.dimer_plddt_avg
}

// Persist across route changes AND reloads within the tab session, so returning
// from a structure page (or refreshing) keeps the streamed results.
const STORAGE_KEY = 'stanza:search'

interface Persisted {
  query: string
  results: Complex[]
  source: SearchSource | null
}

function loadPersisted(): Persisted | null {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY)
    return raw ? (JSON.parse(raw) as Persisted) : null
  } catch {
    return null
  }
}

/**
 * SearchProvider holds the target-search state (query, streamed results, status)
 * above the router, so navigating to a structure page and back does not discard
 * the results. Kept in sessionStorage too, so a reload restores them.
 */
export function SearchProvider({ children }: { children: ReactNode }) {
  const persisted = loadPersisted()

  const [query, setQuery] = useState(persisted?.query ?? '')
  const [results, setResults] = useState<Complex[]>(persisted?.results ?? [])
  // If we restored results, present them as a completed search rather than idle.
  const [status, setStatus] = useState<Status>(
    persisted?.results?.length ? 'done' : 'idle',
  )
  const [source, setSource] = useState<SearchSource | null>(persisted?.source ?? null)
  const [error, setError] = useState<string | null>(null)
  const [online, setOnline] = useState<boolean | null>(null)

  const cancelRef = useRef<(() => void) | null>(null)

  // Probe the backend once so the UI can flag it being down.
  useEffect(() => {
    const ctrl = new AbortController()
    checkHealth(ctrl.signal).then(setOnline)
    return () => ctrl.abort()
  }, [])

  // Cancel any live SSE stream only when the whole app tears down.
  useEffect(() => () => cancelRef.current?.(), [])

  // Mirror results to sessionStorage so a reload can restore them.
  useEffect(() => {
    try {
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ query, results, source }))
    } catch {
      /* storage full or unavailable — non-fatal */
    }
  }, [query, results, source])

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

  return (
    <SearchContext.Provider
      value={{ query, setQuery, results, status, source, error, online, run }}
    >
      {children}
    </SearchContext.Provider>
  )
}

export function useSearch(): SearchState {
  const ctx = useContext(SearchContext)
  if (!ctx) throw new Error('useSearch must be used within a SearchProvider')
  return ctx
}
