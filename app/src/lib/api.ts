/**
 * Client for the Stanza Go API.
 *
 * Endpoints (proxied to the Go server in dev via vite.config.ts):
 *   GET /health          -> { status: "ok" }
 *   GET /search?q=...     -> Server-Sent Events stream of Complex results
 *   GET /complex/:id      -> a single Complex (id = UniProt or AlphaFold ID)
 */

/** Mirrors models.Complex (JSON tags) from the Go backend. */
export type Complex = {
  alphafold_id: string
  uniprot_id: string
  protein_name: string
  gene_name: string
  organism: string
  organism_id: number
  is_who_pathogen: boolean
  disease_associations: string[] | null
  monomer_plddt_avg: number
  dimer_plddt_avg: number
  disorder_delta: number
  drug_count: number
  known_drug_names: string[] | null
  monomer_structure_url: string
  complex_structure_url: string
  category: string
  review_status: string
}

export type SearchSource = 'live' | 'fallback'

export type SearchCallbacks = {
  onResult: (complex: Complex) => void
  onDone: (source: SearchSource) => void
  onError: (message: string) => void
}

/**
 * Open an SSE search stream. Results arrive incrementally as the backend
 * enriches each protein. Returns a `cancel` function; call it to close the
 * stream (on unmount or when starting a new search).
 */
export function searchComplexes(
  query: string,
  cb: SearchCallbacks,
): () => void {
  const es = new EventSource(`/search?q=${encodeURIComponent(query)}`)
  let closed = false
  const close = () => {
    if (!closed) {
      closed = true
      es.close()
    }
  }

  es.addEventListener('result', (event) => {
    try {
      cb.onResult(JSON.parse((event as MessageEvent).data) as Complex)
    } catch {
      /* skip malformed frame */
    }
  })

  es.addEventListener('done', (event) => {
    let source: SearchSource = 'live'
    try {
      source = (JSON.parse((event as MessageEvent).data) as { source: SearchSource })
        .source
    } catch {
      /* keep default */
    }
    cb.onDone(source)
    close()
  })

  // Fires for both server-sent `event: error` frames (which carry data) and
  // native connection failures (which don't). EventSource auto-reconnects on
  // native errors, so we must close() to stop it re-running the search.
  es.addEventListener('error', (event) => {
    if (closed) return
    const data = (event as MessageEvent).data
    if (data) {
      try {
        cb.onError((JSON.parse(data) as { error: string }).error)
      } catch {
        cb.onError('search failed')
      }
    } else {
      cb.onError('Could not reach the search service.')
    }
    close()
  })

  return close
}

/** Fetch full detail for one complex, including drug coverage. */
export async function getComplex(
  id: string,
  signal?: AbortSignal,
): Promise<Complex> {
  const res = await fetch(`/complex/${encodeURIComponent(id)}`, { signal })
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(body.error ?? `Request failed (${res.status})`)
  }
  return (await res.json()) as Complex
}

/** Ping the backend. Resolves true when the API is reachable and healthy. */
export async function checkHealth(signal?: AbortSignal): Promise<boolean> {
  try {
    const res = await fetch('/health', { signal })
    if (!res.ok) return false
    const body = (await res.json()) as { status?: string }
    return body.status === 'ok'
  } catch {
    return false
  }
}
