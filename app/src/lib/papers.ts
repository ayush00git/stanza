/**
 * Client for the Stanza paper-ingestion endpoints (the `/papers` flow).
 *
 * A paper is uploaded as a PDF, Claude reads it and proposes a curated-site draft
 * (an ExtractedSite) with every field carrying the sentence it was drawn from, and a
 * human confirms or corrects each field before it drives a run. The provenance is the
 * whole point: an extracted number feeds docking, generation, and the weight gate
 * downstream, so the model proposes and a person ratifies.
 *
 * Endpoints (proxied to the Go server in dev via vite.config.ts):
 *   POST /papers/extract   (multipart, field "file") -> { extraction: ExtractedSite }
 *   POST /papers/confirm   (JSON, the edited site)    -> { uniprot_id, mutation }
 */

/**
 * A curated-site draft Claude pulled from a paper. Mirrors models.ExtractedSite
 * (JSON tags) from the Go backend. Every value ships beside the exact sentence it
 * came from in `citations`, keyed by the JSON field name below.
 */
export type ExtractedSite = {
  /** Target identity. "" if the paper does not name one. */
  uniprot_id: string
  protein_name: string
  /** e.g. "C797S"; "" if the target is a wild-type residue. */
  mutation: string
  /** The residue a covalent warhead should bond, e.g. "Cys797". "" for a non-covalent target. */
  reactive_residue: string
  covalent: boolean

  /** Design guidance — maps onto services.SiteGuidance server-side. */
  mechanism: string
  pharmacophore: string
  /** Weight window floor/ceiling, Da. */
  min_mw: number
  max_mw: number
  /** Published inhibitors the generator must not re-derive. */
  prior_art: string[]
  /** UniProt-numbered residues lining the pocket. */
  pocket_residues: number[]

  /** Structure — the PDB the WT/mutant pair is built on. "" if the paper names none. */
  pdb_id: string
  chain: string

  /**
   * One verbatim source sentence per field above, keyed by the JSON field name
   * (e.g. "reactive_residue", "min_mw", "pdb_id"). A field the model could not
   * ground in the paper is left out of this map rather than cited to nothing.
   */
  citations: Record<string, string>

  /** The model's own flags: which fields it was unsure of, what to double-check. */
  notes: string
}

/** Extract an { error } message from a failed JSON response, with a fallback. */
async function errorMessage(res: Response, fallback: string): Promise<string> {
  const body = (await res.json().catch(() => ({}))) as { error?: string }
  return body.error ?? `${fallback} (${res.status})`
}

/**
 * Upload a paper PDF and have Claude extract a curated-site draft from it. The
 * response is either { extraction: ExtractedSite } or the bare site; both are
 * handled. Slow — one Claude call over the whole document. Wraps POST /papers/extract.
 */
export async function extractPaper(file: File): Promise<ExtractedSite> {
  const form = new FormData()
  form.append('file', file)
  const res = await fetch('/papers/extract', { method: 'POST', body: form })
  if (!res.ok) throw new Error(await errorMessage(res, 'Could not read the paper'))
  const body = (await res.json()) as { extraction?: ExtractedSite } & Partial<ExtractedSite>
  return (body.extraction ?? (body as ExtractedSite))
}

/**
 * Confirm an extracted (and possibly human-corrected) site, promoting it into the
 * curated store so it can drive a run. Wraps POST /papers/confirm.
 */
export async function confirmPaper(
  site: ExtractedSite,
): Promise<{ uniprot_id: string; mutation: string }> {
  const res = await fetch('/papers/confirm', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(site),
  })
  if (!res.ok) throw new Error(await errorMessage(res, 'Could not confirm the site'))
  return (await res.json()) as { uniprot_id: string; mutation: string }
}
