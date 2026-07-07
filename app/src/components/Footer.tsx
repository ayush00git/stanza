export default function Footer() {
  return (
    <footer
      id="colophon"
      className="border-t border-hairline"
    >
      <div className="mx-auto flex max-w-5xl flex-col gap-8 px-6 py-14 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <p className="font-display text-2xl font-medium tracking-[-0.02em] text-ink">
            Stanza<span className="text-accent">.</span>
          </p>
          <p className="mt-3 max-w-xs text-sm leading-relaxed text-muted">
            Set in Fraunces and Inter. Built for the words, and nothing more.
          </p>
        </div>

        <div className="flex gap-10 font-mono text-xs uppercase tracking-[0.15em] text-muted">
          <a href="#top" className="transition-colors hover:text-ink">
            Back to top
          </a>
          <a href="#features" className="transition-colors hover:text-ink">
            The studio
          </a>
          <a href="#anthology" className="transition-colors hover:text-ink">
            Anthology
          </a>
        </div>
      </div>
    </footer>
  )
}
