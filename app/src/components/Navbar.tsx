const links = [
  { label: 'Pipeline', href: '#pipeline' },
  { label: 'Data', href: '#data' },
  { label: 'Docs', href: '#colophon' },
]

export default function Navbar() {
  return (
    <header className="sticky top-0 z-50 border-b border-hairline bg-paper/85 backdrop-blur-sm">
      <nav className="mx-auto flex max-w-5xl items-center justify-between px-6 py-4">
        <a
          href="#top"
          className="font-display text-xl font-medium tracking-[-0.02em] text-ink"
        >
          Stanza
          <span className="text-accent">.</span>
        </a>

        <ul className="hidden items-center gap-8 sm:flex">
          {links.map((link) => (
            <li key={link.href}>
              <a
                href={link.href}
                className="text-sm text-muted transition-colors hover:text-ink"
              >
                {link.label}
              </a>
            </li>
          ))}
        </ul>

        <a
          href="#pipeline"
          className="rounded-full border border-ink px-4 py-1.5 text-sm font-medium text-ink transition-colors hover:bg-ink hover:text-paper"
        >
          Request access
        </a>
      </nav>
    </header>
  )
}
