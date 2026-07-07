import Verse from './Verse'

const openingStanza = [
  'Every thought arrives in pieces:',
  'a line, a breath, a deliberate break,',
  'the turn that makes the meaning land —',
  'Stanza is the room where they cohere.',
]

export default function Hero() {
  return (
    <section id="top" className="mx-auto max-w-5xl px-6 pt-20 pb-24 sm:pt-28">
      <p className="rise mb-10 font-mono text-xs uppercase tracking-[0.25em] text-accent">
        A composition tool for writers
      </p>

      <Verse lines={openingStanza} animate className="max-w-2xl" />

      <div className="mt-14 grid gap-10 sm:grid-cols-[1.4fr_1fr] sm:items-end">
        <p className="max-w-md text-lg leading-relaxed text-muted">
          A quiet, focused studio for poems, essays, and everything with a
          shape. Draft in structured verse, revise line by line, and publish
          the words without the clutter.
        </p>

        <div id="start" className="flex flex-wrap items-center gap-4 sm:justify-end">
          <a
            href="#features"
            className="rounded-full bg-ink px-6 py-3 text-sm font-medium text-paper transition-transform hover:-translate-y-0.5"
          >
            Start a draft
          </a>
          <a
            href="#anthology"
            className="text-sm font-medium text-ink underline decoration-hairline decoration-2 underline-offset-4 transition-colors hover:decoration-accent"
          >
            Read the anthology
          </a>
        </div>
      </div>
    </section>
  )
}
