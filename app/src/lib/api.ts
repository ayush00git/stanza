/**
 * Client for the Stanza Go API.
 *
 * Endpoints (proxied to the Go server in dev via vite.config.ts):
 *   GET /health                      -> { status: "ok" }
 *   GET /search?q=...                -> Server-Sent Events stream of Complex results
 *   GET /complex/:id                 -> a single Complex (id = UniProt or AlphaFold ID)
 *   GET /complex/:id/binding-sites   -> fpocket BindingSiteResult (monomer + dimer)
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

/** Per-residue confidence for a pocket residue. Mirrors models.ResidueConfidence. */
export type ResidueConfidence = {
  residue_index: number
  chain: string
  monomer_plddt: number
  dimer_plddt: number
  delta: number
}

/** A druggable pocket detected by fpocket. Mirrors models.Pocket. */
export type Pocket = {
  pocket_id: number
  druggability_score: number
  volume: number
  surface_area: number
  depth: number
  hydrophobicity: number
  polarity: number
  source_type: 'monomer' | 'dimer' | string
  is_interface_pocket: boolean
  is_conserved?: boolean
  is_emergent?: boolean
  avg_disorder_delta: number
  avg_plddt: number
  residue_indices: number[]
  residue_names: string[]
  residue_chains: string[]
  chains?: string[]
  center: [number, number, number]
  residue_confidences: ResidueConfidence[]
}

/** Subset of models.ComparisonResult we surface on the viewer page. */
export type ComparisonResult = {
  ddgi: number
  pocket_mapping: {
    conserved_count: number
    monomer_only_count: number
    emergent_count: number
    interface_count: number
  }
  summary_metrics: {
    total_monomer_pockets: number
    total_dimer_pockets: number
    interface_pocket_count: number
    avg_monomer_druggability: number
    avg_dimer_druggability: number
  }
}

/** Full binding-site analysis for a complex. Mirrors models.BindingSiteResult. */
export type BindingSiteResult = {
  uniprot_id: string
  complex_entry_id: string
  total_pockets: number
  interface_pocket_count: number
  pockets: Pocket[]
  monomer_total_pockets: number
  monomer_pockets: Pocket[]
  comparison?: ComparisonResult | null
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

/**
 * Run fpocket binding-site analysis for a complex. This is slow — the backend
 * downloads the monomer and dimer structures and runs fpocket on each — so
 * callers should show a pending state and allow a generous timeout.
 */
export async function getBindingSites(
  id: string,
  signal?: AbortSignal,
): Promise<BindingSiteResult> {
  const res = await fetch(`/complex/${encodeURIComponent(id)}/binding-sites`, {
    signal,
  })
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(body.error ?? `Binding-site analysis failed (${res.status})`)
  }
  return (await res.json()) as BindingSiteResult
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
