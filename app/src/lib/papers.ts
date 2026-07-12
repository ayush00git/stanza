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

/** One streamed step of a live extraction: a chunk of Claude's reasoning about the paper. */
export type PaperProgress = { stage: string; thinking?: string }

export type PaperStreamCallbacks = {
  onProgress: (p: PaperProgress) => void
  onExtraction: (site: ExtractedSite) => void
  onError: (message: string) => void
}

/**
 * Stream a paper extraction. Uploads the PDF and reads the response as an SSE stream,
 * surfacing Claude's summarized reasoning live (onProgress) before the final draft lands
 * (onExtraction). It is a POST because the payload is a file, so this reads the response
 * body directly rather than using EventSource (which is GET-only). Wraps
 * POST /papers/extract/stream.
 */
export async function streamExtractPaper(file: File, cb: PaperStreamCallbacks): Promise<void> {
  const form = new FormData()
  form.append('file', file)
  let res: Response
  try {
    res = await fetch('/papers/extract/stream', { method: 'POST', body: form })
  } catch {
    cb.onError('Lost connection to the extraction service.')
    return
  }
  if (!res.ok || !res.body) {
    cb.onError(await errorMessage(res, 'Could not read the paper'))
    return
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    // SSE frames are separated by a blank line.
    let sep: number
    while ((sep = buf.indexOf('\n\n')) !== -1) {
      const frame = buf.slice(0, sep)
      buf = buf.slice(sep + 2)
      let event = 'message'
      let data = ''
      for (const line of frame.split('\n')) {
        if (line.startsWith('event:')) event = line.slice(6).trim()
        else if (line.startsWith('data:')) data += line.slice(5).trim()
      }
      if (!data) continue
      let parsed: unknown
      try {
        parsed = JSON.parse(data)
      } catch {
        continue
      }
      if (event === 'progress') cb.onProgress(parsed as PaperProgress)
      else if (event === 'extraction') {
        const p = parsed as { extraction?: ExtractedSite }
        cb.onExtraction((p.extraction ?? (parsed as ExtractedSite)))
      } else if (event === 'error') cb.onError((parsed as { error?: string }).error ?? 'Extraction failed')
      // `done` needs no handling; the stream ends after it.
    }
  }
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
