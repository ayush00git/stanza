/**
 * Client for the Stanza Go API.
 *
 * Endpoints (proxied to the Go server in dev via vite.config.ts):
 *   GET /health                      -> { status: "ok" }
 *   GET /search?q=...                -> Server-Sent Events stream of Complex results
 *   GET /complex/:id                 -> a single Complex (id = UniProt or AlphaFold ID)
 *   GET /complex/:id/binding-sites   -> fpocket BindingSiteResult (monomer + dimer)
 *   GET /complex/:id/drugs           -> DrugCoverage (ChEMBL drug count + known drugs)
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

/** ChEMBL drug coverage for a target. Mirrors the /complex/:id/drugs response. */
export type DrugCoverage = { drug_count: number; known_drug_names: string[] }

/**
 * Fetch ChEMBL drug coverage for a complex. This is slow — the backend
 * paginates through every activity page for the target — so it's kept separate
 * from getComplex and fetched lazily; the structure page renders without it and
 * fills the drug info in once this resolves.
 *
 * Wraps GET /complex/:id/drugs.
 */
export async function getDrugCoverage(
  id: string,
  signal?: AbortSignal,
): Promise<DrugCoverage> {
  const res = await fetch(`/complex/${encodeURIComponent(id)}/drugs`, { signal })
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(body.error ?? `Drug coverage lookup failed (${res.status})`)
  }
  return (await res.json()) as DrugCoverage
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
  name?: string
  smiles?: string
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

/* ────────────────────────────────────────────────────────────────────────
   Resistance-design runs (the `/runs` flow)

   A run pins a UniProt target + a resistance mutation and drives the pipeline:
   acquire the wild-type structure, build a matched WT/mutant pair, analyse the
   two pockets and their delta, generate candidate molecules with Claude
   (RDKit-filtered), dock them into both pockets, and rank by selectivity.
   These mirror the Go models in models/run.go, models/comparison.go and the
   scoring package. Separate from the /complex oligomerization flow above.
   ──────────────────────────────────────────────────────────────────────── */

/** A parsed point substitution, e.g. "G12C". Mirrors models.Mutation. */
export type Mutation = {
  raw: string
  wild_type: string
  position: number
  mutant: string
}

/** The wild-type structure chosen for a run (Stage 1). Mirrors models.WTStructure. */
export type WTStructure = {
  source: 'pdb_holo' | 'pdb_apo' | 'alphafold' | string
  pdb_id?: string
  alphafold_id?: string
  structure_url: string
  chain?: string
  ligand_count: number
  resolution?: number
  residue_resolved: boolean
  wild_type_matches: boolean
  target_chain?: string
  target_auth_seq_id?: number
  notes?: string[]
}

/** The matched WT/mutant structure pair built by mutagenesis (Stage 2). */
export type MutagenesisResult = {
  tool: string
  wt_structure_url: string
  mutant_structure_url: string
  target_chain: string
  target_residue_number: number
  wild_type_residue: string // 3-letter, e.g. "GLY"
  mutant_residue: string // 3-letter, e.g. "CYS"
  notes?: string[]
}

/** The resistance pocket to design against. Mirrors models.MutantPocket. */
export type MutantPocket = {
  key_residues: string[]
  volume: number
  hydrophobicity: number
  polarity?: number
  center: [number, number, number]
  pocket_id: number
}

/** What the mutation did to the pocket (WT → mutant). Mirrors models.PocketDelta. */
export type PocketDelta = {
  changed: string[]
  residues_gained?: string[]
  residues_lost?: string[]
  d_volume: number
  d_hydrophobicity: number
  d_polarity: number
  hbonds_gained?: string[]
  hbonds_lost?: string[]
  effect: string
}

/** The resistance-pocket payload conditioning generation. Mirrors models.MutantPocketContext. */
export type MutantPocketContext = {
  mutant_pocket: MutantPocket
  pocket_delta: PocketDelta
}

/** Stage-3 pocket analysis for a run (both tracks + delta). Mirrors models.PocketAnalysis. */
export type RunPocketAnalysis = {
  wt_pockets: Pocket[]
  mutant_pockets: Pocket[]
  conserved_count: number
  wt_only_count: number
  emergent_count: number
  context?: MutantPocketContext | null
}

/**
 * One molecule docked into BOTH tracks of a run (Stage 4). A single dock returns
 * paired affinities, the selectivity margin, and both poses. Mirrors
 * models.LigandDock. Sign convention: Vina kcal/mol, more negative = tighter;
 * selectivity = wt_score − mutant_score, large positive = mutant-selective.
 * mutant_score is the RAW Vina affinity (no covalent adjustment). For a covalent
 * target the non-covalent selectivity reads ≈0 — a Gly12→Cys12 swap barely
 * perturbs reversible binding — and that is correct; the covalent evidence lives
 * in `covalent`, not here.
 */
/**
 * Whether a warhead can actually attack the mutated cysteine. Vina scores
 * non-covalently and cannot see the bond that gives a covalent inhibitor its
 * WT/mutant selectivity, so this records the one thing a docked pose can prove:
 * the geometry — does the warhead's electrophilic carbon reach the thiol, along a
 * trajectory that permits nucleophilic attack, in a pose the receptor binds?
 * Mirrors models.CovalentDock. Present only for warhead-bearing molecules on a
 * cysteine target; mutant_pose_pdb is the tethered adduct only when status is
 * 'tethered'.
 */
/**
 * Why a warhead can or cannot bond the thiol. A warhead that cannot reach the
 * thiol and a warhead whose measurement failed are different facts, and both
 * differ from a molecule carrying no warhead at all (which has no CovalentDock).
 */
export type CovalentStatus =
  | 'tethered' // geometry permits the bond; a valid adduct pose was built
  | 'feasible' // geometry permits the bond; the adduct pose was rejected
  | 'infeasible' // warhead present but cannot attack the thiol (too far or wrong angle)
  | 'unreadable_pose' // no docked mode could be mapped onto the ligand
  | 'assess_failed' // the assessment itself errored
  | 'no_thiol' // the target residue carries no SG

export type CovalentDock = {
  target_residue: string // e.g. "Cys12"
  warhead_type?: string // e.g. "acrylamide"
  status: CovalentStatus
  // Feasibility is DIMENSIONLESS, not an energy. Covalent potency is kinetic
  // (kinact/KI), never a ΔG, so this is a 0–1 geometric plausibility, not kcal/mol.
  // 0 = the warhead cannot attack the thiol.
  feasibility: number // 0–1
  reach_distance?: number // MEDIAN warhead-C → thiol-SG over replicate seeds (Å)
  reach_spread?: number // max − min reach across replicates (Å)
  attack_angle?: number // approach angle at the electrophilic carbon (degrees; ~105° Michael, ~180° SN2)
  mode_rank?: number // 1-based Vina mode the geometry came from
  mode_affinity?: number // that mode's Vina affinity (kcal/mol)
  replicates?: number // docking seeds the geometry was measured over
  bond_distance?: number // S–C of the tethered adduct pose (Å)
  /** The covalent call flips with the docking seed — treat as indistinguishable, not ranked. */
  uncertain?: boolean
  note?: string // why a tether or an assessment failed
}

/** Whether the warhead can attack the thiol — a geometry verdict, not an energy credit. */
export function isCovalentFeasible(c: CovalentDock): boolean {
  return c.status === 'tethered' || c.status === 'feasible'
}

export type LigandDock = {
  smiles: string
  wt_score: number
  mutant_score: number
  selectivity: number
  wt_pose_pdb?: string
  mutant_pose_pdb?: string
  covalent?: CovalentDock
}

/** A Claude-proposed molecule that passed RDKit validation (Stage 5/6). Mirrors models.Candidate. */
export type Candidate = {
  smiles: string
  inchikey: string
  qed: number
  ro5_pass: boolean
  sa_score?: number
  mol_weight: number
  logp: number
}

/** A resistance-design run. Mirrors models.Run. */
export type Run = {
  id: string
  uniprot_id: string
  mutation: Mutation
  site_hint?: string
  status: string
  wt_structure?: WTStructure
  mutagenesis?: MutagenesisResult
  pockets?: RunPocketAnalysis
  docks?: LigandDock[]
  candidates?: Candidate[]
  error?: string
  created_at: string
}

/** Selectivity scorecard for one docked molecule (Stage 7). Mirrors scoring.Scores. */
export type Scores = {
  smiles: string
  mutant_score: number
  wt_score: number
  selectivity: number
  qed?: number | null
  fitness?: number | null
  status: 'scored' | 'incomplete' | string
  /** 0–1 geometric plausibility that the warhead can bond the thiol; mirrors CovalentDock.feasibility. Dimensionless, not an energy. */
  covalent_feasibility?: number | null
  /** Present for warhead-bearing molecules on a cysteine target — the covalent geometry verdict, not a score adjustment. */
  covalent?: CovalentDock
}

/** One row of the ranked leaderboard. Mirrors scoring.RankedMolecule. */
export type RankedMolecule = {
  rank: number
  selected: boolean
  smiles: string
  scores: Scores
}

/** Fitness term weights. Mirrors scoring.FitnessWeights. */
export type FitnessWeights = {
  potency: number
  /** Non-covalent WT/mutant margin. ≈0 for a covalent target, so weighted lightly. */
  selectivity: number
  drug_likeness: number
  /** Dimensionless covalent-attack geometry (0–1) — the only covalent evidence a dock yields. */
  covalent_feasibility: number
}

/** The computed selectivity leaderboard for a run (Stage 7). Mirrors scoring.Ranking. */
export type Ranking = {
  run_id: string
  weights: FitnessWeights
  normalization: 'zscore' | 'minmax' | string
  count: number
  ranked: RankedMolecule[]
  excluded: Scores[]
}

/** Extract an { error } message from a failed JSON response, with a fallback. */
async function errorMessage(res: Response, fallback: string): Promise<string> {
  const body = (await res.json().catch(() => ({}))) as { error?: string }
  return body.error ?? `${fallback} (${res.status})`
}

/** The served PDB URL for a run's generated structure track ("wt" | "mutant"). */
export function runStructureUrl(id: string, track: 'wt' | 'mutant'): string {
  return `/runs/${encodeURIComponent(id)}/structure/${track}`
}

/** Create a resistance-design run (Stage 1–2 run synchronously server-side). Wraps POST /runs. */
export async function createRun(
  body: {
    uniprot_id: string
    mutation: string
    site_hint?: string
    profile_id?: string
  },
  signal?: AbortSignal,
): Promise<Run> {
  const res = await fetch('/runs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw new Error(await errorMessage(res, 'Could not create run'))
  return (await res.json()) as Run
}

/** List all runs, newest-first. Wraps GET /runs. */
export async function listRuns(signal?: AbortSignal): Promise<Run[]> {
  const res = await fetch('/runs', { signal })
  if (!res.ok) throw new Error(await errorMessage(res, 'Could not list runs'))
  const body = (await res.json()) as { runs: Run[] | null }
  return body.runs ?? []
}

/** List the runs linked to one profile, newest-first. Wraps GET /runs?profile_id=<id>. */
export async function listRunsByProfile(
  profileId: string,
  signal?: AbortSignal,
): Promise<Run[]> {
  const res = await fetch(
    `/runs?profile_id=${encodeURIComponent(profileId)}`,
    { signal },
  )
  if (!res.ok) throw new Error(await errorMessage(res, 'Could not list runs'))
  const body = (await res.json()) as { runs: Run[] | null }
  return body.runs ?? []
}

/** Fetch one run. Wraps GET /runs/:id. */
export async function getRun(id: string, signal?: AbortSignal): Promise<Run> {
  const res = await fetch(`/runs/${encodeURIComponent(id)}`, { signal })
  if (!res.ok) throw new Error(await errorMessage(res, 'Run not found'))
  return (await res.json()) as Run
}

/**
 * Run (or fetch cached) Stage-3 WT/mutant pocket analysis + delta. Slow the first
 * time — the backend runs fpocket on both structures. Wraps GET /runs/:id/pockets.
 */
export async function getRunPockets(
  id: string,
  signal?: AbortSignal,
): Promise<RunPocketAnalysis> {
  const res = await fetch(`/runs/${encodeURIComponent(id)}/pockets`, { signal })
  if (!res.ok) throw new Error(await errorMessage(res, 'Pocket analysis failed'))
  return (await res.json()) as RunPocketAnalysis
}

/**
 * Generate candidate molecules with Claude for a run's mutant pocket (Stage 6),
 * RDKit-filtered (Stage 5). Returns only the kept, scored candidates. Slow — one
 * Claude call (+ pocket analysis if not yet done). Wraps POST /runs/:id/generate.
 */
export async function generateCandidates(
  id: string,
  n?: number,
  signal?: AbortSignal,
): Promise<Candidate[]> {
  const res = await fetch(`/runs/${encodeURIComponent(id)}/generate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(n != null ? { n } : {}),
    signal,
  })
  if (!res.ok) throw new Error(await errorMessage(res, 'Generation failed'))
  const body = (await res.json()) as { candidates: Candidate[] | null }
  return body.candidates ?? []
}

/**
 * Dock one SMILES into both the WT and mutant pockets (Stage 4). Synchronous:
 * resolves with the paired scores + both poses (per-SMILES cached server-side, so
 * re-docking the same molecule is instant). Wraps POST /runs/:id/dock.
 */
export async function dockRunLigand(
  id: string,
  smiles: string,
  signal?: AbortSignal,
): Promise<LigandDock> {
  const res = await fetch(`/runs/${encodeURIComponent(id)}/dock`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ligand_smiles: smiles }),
    signal,
  })
  if (!res.ok) throw new Error(await errorMessage(res, 'Docking failed'))
  return (await res.json()) as LigandDock
}

/**
 * Fetch the run's selectivity fitness leaderboard (Stage 7). Optional overrides:
 * norm (zscore|minmax), top (how many flagged selected), and wp/ws/wq weights.
 * Wraps GET /runs/:id/ranking.
 */
export async function getRunRanking(
  id: string,
  opts?: {
    norm?: 'zscore' | 'minmax'
    top?: number
    weights?: FitnessWeights
    signal?: AbortSignal
  },
): Promise<Ranking> {
  const params = new URLSearchParams()
  if (opts?.norm) params.set('norm', opts.norm)
  if (opts?.top != null) params.set('top', String(opts.top))
  if (opts?.weights) {
    params.set('wp', String(opts.weights.potency))
    params.set('ws', String(opts.weights.selectivity))
    params.set('wq', String(opts.weights.drug_likeness))
  }
  const qs = params.toString()
  const res = await fetch(
    `/runs/${encodeURIComponent(id)}/ranking${qs ? `?${qs}` : ''}`,
    { signal: opts?.signal },
  )
  if (!res.ok) throw new Error(await errorMessage(res, 'Ranking failed'))
  return (await res.json()) as Ranking
}

/* ────────────────────────────────────────────────────────────────────────
   Researcher profiles (the `/profiles` flow)

   Auth is intentionally minimal: a profile just lets someone track their run
   history. There's no validation or verification. Mirrors models.Profile.
   ──────────────────────────────────────────────────────────────────────── */

/** A researcher profile. Mirrors models.Profile (JSON tags). */
export type Profile = {
  id: string
  name: string
  email?: string
  institution?: string
  field?: string
  orcid?: string
  created_at: string
}

/**
 * Create a researcher profile. Only `name` is required. Wraps POST /profiles.
 * When the server has no database configured this responds 503 — the thrown
 * error carries that message so callers can surface it.
 */
export async function createProfile(
  body: {
    name: string
    email?: string
    institution?: string
    field?: string
    orcid?: string
  },
  signal?: AbortSignal,
): Promise<Profile> {
  const res = await fetch('/profiles', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw new Error(await errorMessage(res, 'Could not create profile'))
  return (await res.json()) as Profile
}

/** List all profiles, newest-first (empty when there's no database). Wraps GET /profiles. */
export async function listProfiles(signal?: AbortSignal): Promise<Profile[]> {
  const res = await fetch('/profiles', { signal })
  if (!res.ok) throw new Error(await errorMessage(res, 'Could not list profiles'))
  const body = (await res.json()) as { profiles: Profile[] | null }
  return body.profiles ?? []
}

/** Fetch one profile. Wraps GET /profiles/:id. */
export async function getProfile(
  id: string,
  signal?: AbortSignal,
): Promise<Profile> {
  const res = await fetch(`/profiles/${encodeURIComponent(id)}`, { signal })
  if (!res.ok) throw new Error(await errorMessage(res, 'Profile not found'))
  return (await res.json()) as Profile
}
