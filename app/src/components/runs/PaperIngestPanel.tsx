import { useRef, useState, type DragEvent, type ReactNode } from 'react'
import { confirmPaper, extractPaper, type ExtractedSite } from '../../lib/papers'

/** Human-readable file size, e.g. "1.4 MB". */
function fileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

type Props = {
  /** Called once the confirmed site is committed server-side, so the page can start a run. */
  onConfirmed: (uniprot: string, mutation: string) => void
}

type Phase = 'idle' | 'extracting' | 'review' | 'confirming'

/**
 * The provenance line under a field: the verbatim sentence the value was drawn from,
 * or a subtle note when the model could not ground the field in the paper. This is the
 * point of the whole panel — a confirmation is a check against the source text, not an
 * act of faith, so a field with no citation is shown as unsupported rather than hidden.
 */
function Provenance({ cite }: { cite?: string }) {
  if (cite && cite.trim()) {
    return (
      <span className="mt-0.5 flex gap-1.5 border-l-2 border-hairline pl-2 text-xs italic leading-snug text-muted">
        <span aria-hidden className="not-italic text-muted/60">
          &ldquo;
        </span>
        <span>{cite}</span>
      </span>
    )
  }
  return <span className="mt-0.5 text-xs italic text-muted/60">not found in paper</span>
}

/** A labelled, editable field with its citation shown underneath. */
function Field({
  label,
  cite,
  className,
  children,
}: {
  label: string
  cite?: string
  className?: string
  children: ReactNode
}) {
  return (
    <label className={`flex flex-col gap-1.5 ${className ?? ''}`}>
      <span className="text-xs text-muted">{label}</span>
      {children}
      <Provenance cite={cite} />
    </label>
  )
}

/** A small uppercase group header, matching the house style. */
function GroupHeader({ children }: { children: ReactNode }) {
  return <h3 className="text-xs uppercase tracking-wide text-muted">{children}</h3>
}

const inputCls =
  'rounded-md border border-hairline bg-paper px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-[var(--color-accent)]'

/**
 * PaperIngestPanel — the PDF front door to a run. A paper is uploaded, Claude reads it
 * and proposes a curated-site draft, and every field is presented editable beside the
 * exact sentence it came from. Nothing here drives a run until a human confirms it: an
 * extracted number feeds docking, generation and the weight gate downstream, so the
 * model proposes and a person ratifies. Presentation only; the page owns what happens
 * after a site is confirmed (via onConfirmed).
 */
export default function PaperIngestPanel({ onConfirmed }: Props) {
  const [phase, setPhase] = useState<Phase>('idle')
  const [file, setFile] = useState<File | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [site, setSite] = useState<ExtractedSite | null>(null)
  // Array fields edit as raw text so intermediate states (a trailing comma, a blank line)
  // are allowed; they are parsed back into arrays only at confirm time.
  const [priorArtText, setPriorArtText] = useState('')
  const [pocketText, setPocketText] = useState('')
  const [dragging, setDragging] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  /** Accept a dropped/picked file only if it looks like a PDF. */
  const acceptFile = (f: File | null | undefined) => {
    if (!f) return
    if (!f.name.toLowerCase().endsWith('.pdf')) {
      setError('That is not a PDF. Upload the paper as a .pdf file.')
      return
    }
    setFile(f)
    setError(null)
  }

  const onDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    setDragging(false)
    if (phase === 'extracting') return
    acceptFile(e.dataTransfer.files?.[0])
  }

  /** Merge a patch into the edited site. */
  const update = (patch: Partial<ExtractedSite>) =>
    setSite((prev) => (prev ? { ...prev, ...patch } : prev))

  const handleExtract = () => {
    if (!file || phase === 'extracting') return
    setPhase('extracting')
    setError(null)
    extractPaper(file)
      .then((s) => {
        setSite(s)
        setPriorArtText((s.prior_art ?? []).join('\n'))
        setPocketText((s.pocket_residues ?? []).join(', '))
        setPhase('review')
      })
      .catch((e: unknown) => {
        setError(e instanceof Error ? e.message : 'Could not read the paper')
        setPhase('idle')
      })
  }

  const handleConfirm = () => {
    if (!site || phase === 'confirming') return
    const edited: ExtractedSite = {
      ...site,
      prior_art: priorArtText
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean),
      pocket_residues: pocketText
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)
        .map(Number)
        .filter((n) => Number.isFinite(n)),
    }
    setPhase('confirming')
    setError(null)
    confirmPaper(edited)
      .then((res) => {
        onConfirmed(res.uniprot_id || edited.uniprot_id, res.mutation || edited.mutation)
      })
      .catch((e: unknown) => {
        setError(e instanceof Error ? e.message : 'Could not confirm the site')
        setPhase('review')
      })
  }

  const cites = site?.citations ?? {}

  return (
    <div className="rounded-lg border border-hairline bg-paper-deep/40 p-5">
      {/* Upload + extract. Shown until an extraction lands. */}
      {(phase === 'idle' || phase === 'extracting') && (
        <div>
          <p className="text-sm text-muted">
            Upload a paper as a PDF. Claude reads it and drafts a curated site (target,
            reactive residue, pocket, weight window) with the sentence behind each field, for
            you to confirm before it drives a run.
          </p>

          {/* Dropzone: a whole clickable/droppable area, not a bare file input. */}
          <div
            role="button"
            tabIndex={0}
            onClick={() => phase !== 'extracting' && inputRef.current?.click()}
            onKeyDown={(e) => {
              if ((e.key === 'Enter' || e.key === ' ') && phase !== 'extracting') {
                e.preventDefault()
                inputRef.current?.click()
              }
            }}
            onDragOver={(e) => {
              e.preventDefault()
              if (phase !== 'extracting') setDragging(true)
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={onDrop}
            className={`mt-4 flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed px-6 py-8 text-center transition-colors ${
              phase === 'extracting'
                ? 'cursor-default border-hairline bg-paper-deep/60'
                : dragging
                  ? 'cursor-pointer border-accent bg-accent-soft'
                  : 'cursor-pointer border-hairline bg-paper hover:border-ink'
            }`}
          >
            <input
              ref={inputRef}
              type="file"
              accept=".pdf,application/pdf"
              onChange={(e) => acceptFile(e.target.files?.[0])}
              disabled={phase === 'extracting'}
              className="hidden"
            />
            {phase === 'extracting' ? (
              // The Claude call reads the whole document, so this runs a minute or two.
              <div className="flex flex-col items-center gap-3 py-1">
                <svg viewBox="0 0 24 24" className="h-6 w-6 animate-spin text-claude" fill="none">
                  <circle cx="12" cy="12" r="9" stroke="currentColor" strokeOpacity="0.2" strokeWidth="3" />
                  <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
                </svg>
                <p className="text-sm font-medium text-claude-deep">Reading the paper…</p>
                <p className="text-xs text-muted">
                  Claude is working through the full document. This usually takes a minute or two.
                </p>
                {file && <p className="text-xs text-muted">{file.name}</p>}
              </div>
            ) : file ? (
              <>
                <svg viewBox="0 0 24 24" className="h-7 w-7 text-accent" fill="none" stroke="currentColor" strokeWidth="1.6">
                  <path d="M14 3v4a1 1 0 0 0 1 1h4" strokeLinecap="round" strokeLinejoin="round" />
                  <path d="M5 3h9l5 5v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z" strokeLinejoin="round" />
                </svg>
                <p className="text-sm font-medium text-ink">{file.name}</p>
                <p className="text-xs text-muted">{fileSize(file.size)} · click to choose a different file</p>
              </>
            ) : (
              <>
                <svg viewBox="0 0 24 24" className="h-7 w-7 text-muted" fill="none" stroke="currentColor" strokeWidth="1.6">
                  <path d="M12 16V4m0 0 4 4m-4-4-4 4" strokeLinecap="round" strokeLinejoin="round" />
                  <path d="M4 16v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" strokeLinecap="round" />
                </svg>
                <p className="text-sm font-medium text-ink">Drop a PDF here, or click to browse</p>
                <p className="text-xs text-muted">A medicinal-chemistry paper describing the target and its inhibitors</p>
              </>
            )}
          </div>

          <div className="mt-4 flex items-center justify-end">
            <button
              type="button"
              onClick={handleExtract}
              disabled={!file || phase === 'extracting'}
              className="rounded-md border border-ink bg-ink px-4 py-2 text-sm font-medium text-paper transition-colors hover:bg-transparent hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
            >
              {phase === 'extracting' ? 'Reading…' : 'Extract with Claude'}
            </button>
          </div>

          {error && <p className="mt-3 text-sm text-conf-verylow">{error}</p>}
        </div>
      )}

      {/* Editable confirm view. */}
      {(phase === 'review' || phase === 'confirming') && site && (
        <div className="flex flex-col gap-6">
          <div className="flex items-start gap-2">
            <span className="mt-0.5 h-1.5 w-1.5 flex-none rounded-full bg-claude" />
            <p className="text-sm text-ink">
              <span className="font-medium text-claude-deep">Claude extracted these from the paper.</span>{' '}
              Confirm or correct each before it drives a run. Every field shows the sentence it
              came from.
            </p>
          </div>

          {/* The model's own flags: what it was unsure of, what the paper did not state. */}
          {site.notes.trim() && (
            <div className="rounded-md border border-claude/40 bg-claude/5 px-3.5 py-3">
              <p className="text-xs font-medium uppercase tracking-wide text-claude-deep">
                Notes from Claude
              </p>
              <p className="mt-1 text-sm leading-relaxed text-ink">{site.notes}</p>
            </div>
          )}

          {/* Target identity. */}
          <div className="flex flex-col gap-4">
            <GroupHeader>Target</GroupHeader>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="UniProt ID" cite={cites.uniprot_id}>
                <input
                  value={site.uniprot_id}
                  onChange={(e) => update({ uniprot_id: e.target.value })}
                  placeholder="P00533"
                  spellCheck={false}
                  className={inputCls}
                />
              </Field>
              <Field label="Protein name" cite={cites.protein_name}>
                <input
                  value={site.protein_name}
                  onChange={(e) => update({ protein_name: e.target.value })}
                  className={inputCls}
                />
              </Field>
              <Field label="Mutation" cite={cites.mutation}>
                <input
                  value={site.mutation}
                  onChange={(e) => update({ mutation: e.target.value })}
                  placeholder="C797S"
                  spellCheck={false}
                  className={inputCls}
                />
              </Field>
              <Field label="Reactive residue" cite={cites.reactive_residue}>
                <input
                  value={site.reactive_residue}
                  onChange={(e) => update({ reactive_residue: e.target.value })}
                  placeholder="Cys797"
                  spellCheck={false}
                  className={inputCls}
                />
              </Field>
            </div>
            <div className="flex flex-col gap-1.5">
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={site.covalent}
                  onChange={(e) => update({ covalent: e.target.checked })}
                  className="h-4 w-4 rounded border-hairline accent-[var(--color-accent)]"
                />
                <span className="text-xs text-muted">Covalent (a warhead bonds the reactive residue)</span>
              </label>
              <Provenance cite={cites.covalent} />
            </div>
          </div>

          {/* Design guidance. */}
          <div className="flex flex-col gap-4">
            <GroupHeader>Design guidance</GroupHeader>
            <Field label="Mechanism" cite={cites.mechanism}>
              <textarea
                value={site.mechanism}
                onChange={(e) => update({ mechanism: e.target.value })}
                rows={3}
                className={`${inputCls} resize-y leading-relaxed`}
              />
            </Field>
            <Field label="Pharmacophore" cite={cites.pharmacophore}>
              <textarea
                value={site.pharmacophore}
                onChange={(e) => update({ pharmacophore: e.target.value })}
                rows={2}
                className={`${inputCls} resize-y leading-relaxed`}
              />
            </Field>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="Min MW (Da)" cite={cites.min_mw}>
                <input
                  type="number"
                  value={site.min_mw || ''}
                  onChange={(e) => update({ min_mw: e.target.value === '' ? 0 : Number(e.target.value) })}
                  placeholder="430"
                  className={`${inputCls} tabular-nums`}
                />
              </Field>
              <Field label="Max MW (Da)" cite={cites.max_mw}>
                <input
                  type="number"
                  value={site.max_mw || ''}
                  onChange={(e) => update({ max_mw: e.target.value === '' ? 0 : Number(e.target.value) })}
                  placeholder="620"
                  className={`${inputCls} tabular-nums`}
                />
              </Field>
            </div>
            <Field label="Prior art (one inhibitor per line)" cite={cites.prior_art}>
              <textarea
                value={priorArtText}
                onChange={(e) => setPriorArtText(e.target.value)}
                rows={3}
                placeholder="osimertinib&#10;afatinib"
                spellCheck={false}
                className={`${inputCls} resize-y leading-relaxed`}
              />
            </Field>
            <Field label="Pocket residues (UniProt-numbered, comma separated)" cite={cites.pocket_residues}>
              <input
                value={pocketText}
                onChange={(e) => setPocketText(e.target.value)}
                placeholder="718, 790, 797, 855"
                spellCheck={false}
                className={`${inputCls} tabular-nums`}
              />
            </Field>
          </div>

          {/* Structure. */}
          <div className="flex flex-col gap-4">
            <GroupHeader>Structure</GroupHeader>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="PDB ID" cite={cites.pdb_id}>
                <input
                  value={site.pdb_id}
                  onChange={(e) => update({ pdb_id: e.target.value })}
                  placeholder="6OIM"
                  spellCheck={false}
                  className={inputCls}
                />
              </Field>
              <Field label="Chain" cite={cites.chain}>
                <input
                  value={site.chain}
                  onChange={(e) => update({ chain: e.target.value })}
                  placeholder="A"
                  spellCheck={false}
                  className={inputCls}
                />
              </Field>
            </div>
          </div>

          {error && <p className="text-sm text-conf-verylow">{error}</p>}

          <div className="flex flex-wrap items-center gap-3 border-t border-hairline pt-4">
            <button
              type="button"
              onClick={handleConfirm}
              disabled={phase === 'confirming'}
              className="rounded-md border border-ink bg-ink px-4 py-2 text-sm font-medium text-paper transition-colors hover:bg-transparent hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
            >
              {phase === 'confirming' ? 'Creating…' : 'Confirm and create run'}
            </button>
            <button
              type="button"
              onClick={() => {
                setPhase('idle')
                setSite(null)
                setError(null)
              }}
              disabled={phase === 'confirming'}
              className="text-sm text-muted transition-colors hover:text-ink disabled:opacity-50"
            >
              Start over
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
