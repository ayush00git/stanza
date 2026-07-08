/**
 * Client for the Stanza Go API.
 *
 * Endpoints (proxied to the Go server in dev via vite.config.ts):
 *   GET /health                      -> { status: "ok" }
 *   GET /search?q=...                -> Server-Sent Events stream of Complex results
 *   GET /complex/:id                 -> a single Complex (id = UniProt or AlphaFold ID)
 *   GET /complex/:id/binding-sites   -> fpocket BindingSiteResult (monomer + dimer)
 *   GET /chembl?pocket_id=...        -> Fragment[] candidate ligands for a pocket
 *   POST /dock                       -> { job_id } (202 Accepted; docking runs async)
 *   GET /dock/status?id=...          -> DockingResult (poll until status is done/error)
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

/**
 * A candidate small molecule suggested from ChEMBL for a pocket.
 * Mirrors models.Fragment (JSON tags) from the Go backend.
 */
export type Fragment = {
  chembl_id: string
  name: string
  smiles: string
  mol_weight: number
  logp: number
  similarity_score: number
}

/**
 * Fetch candidate ChEMBL fragments for a pocket. The backend maps pocket
 * geometry/chemistry (volume, hydrophobicity, polarity) onto ChEMBL molecule
 * filters, so pass overrides straight from the binding-site table row. The
 * pocket must already exist in the server-side pocket store (i.e. binding-site
 * analysis has been run for the complex) or the request 404s.
 *
 * Wraps GET /chembl?pocket_id=<int>&source_type=<monomer|dimer>&volume&hydrophobicity&polarity
 */
export async function getChemblFragments(
  pocketId: number,
  opts?: {
    sourceType?: string
    volume?: number
    hydrophobicity?: number
    polarity?: number
    signal?: AbortSignal
  },
): Promise<Fragment[]> {
  const params = new URLSearchParams({ pocket_id: String(pocketId) })
  if (opts?.sourceType) params.set('source_type', opts.sourceType)
  if (opts?.volume != null) params.set('volume', String(opts.volume))
  if (opts?.hydrophobicity != null)
    params.set('hydrophobicity', String(opts.hydrophobicity))
  if (opts?.polarity != null) params.set('polarity', String(opts.polarity))

  const res = await fetch(`/chembl?${params.toString()}`, {
    signal: opts?.signal,
  })
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(body.error ?? `ChEMBL fragment lookup failed (${res.status})`)
  }
  return (await res.json()) as Fragment[]
}

/**
 * Body for POST /dock. Mirrors the handler's dockPOSTBody. `pocket_id` and
 * `ligand_smiles` are required; supply either `protein_pdb_path` (a local path
 * or URL) or `protein_pdb_id`. `source_type` defaults to "dimer" server-side.
 */
export type DockSubmitRequest = {
  pocket_id: number
  ligand_smiles: string
  source_type?: string
  protein_pdb_path?: string
  protein_pdb_id?: string
}

/** Response from POST /dock (HTTP 202): the async job identifier to poll. */
export type DockSubmitResponse = {
  job_id: string
}

/** Terminal + in-flight states of a docking job. Mirrors DockingResult.Status. */
export type DockStatus = 'pending' | 'running' | 'done' | 'error'

/** One binding pose returned by Vina. Mirrors services.Conformation. */
export type Conformation = {
  mode: number
  binding_affinity: number
  rmsd_lb: number
  rmsd_ub: number
  pose_pdb: string
}

/**
 * Status + output of a docking job. Mirrors services.DockingResult.
 * While in flight, `status` is 'pending' or 'running' and the result fields are
 * zero-valued; on 'done' `binding_affinity`/`pose_pdb` are populated; on 'error'
 * `error` holds the failure message.
 */
export type DockingResult = {
  job_id: string
  pocket_id: number
  status: DockStatus
  binding_affinity: number
  pose_pdb: string
  error?: string
  conformations?: Conformation[]
}

/**
 * A completed docking pose lifted out of the docking panel so the page can
 * render it in the 3D viewer. `pdb` is the raw PDB content of the docked ligand
 * (from DockingResult.pose_pdb); `source_type` says which structure (monomer or
 * dimer) it belongs in so the right viewer overlays it.
 */
export type DockedPose = {
  pdb: string
  source_type: string
  pocket_id: number
  chembl_id?: string
  binding_affinity?: number
}

/**
 * Submit an asynchronous docking job (SMILES ligand vs. a pocket). Resolves with
 * the job id (HTTP 202); poll getDockStatus with it until the job finishes.
 *
 * Wraps POST /dock with a JSON body.
 */
export async function submitDock(
  req: DockSubmitRequest,
  signal?: AbortSignal,
): Promise<DockSubmitResponse> {
  const res = await fetch('/dock', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
    signal,
  })
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(body.error ?? `Docking submission failed (${res.status})`)
  }
  return (await res.json()) as DockSubmitResponse
}

/**
 * Fetch the current status of a docking job. Poll this until `status` is 'done'
 * or 'error'.
 *
 * Wraps GET /dock/status?id=<jobID>.
 */
export async function getDockStatus(
  jobId: string,
  signal?: AbortSignal,
): Promise<DockingResult> {
  const res = await fetch(`/dock/status?id=${encodeURIComponent(jobId)}`, {
    signal,
  })
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(body.error ?? `Docking status lookup failed (${res.status})`)
  }
  return (await res.json()) as DockingResult
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
