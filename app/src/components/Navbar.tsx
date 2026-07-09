import { Link } from 'react-router-dom'
import { useActiveProfile } from '../lib/profile'

const links = [
  { label: 'Search', href: '#search' },
  { label: 'Pipeline', href: '#pipeline' },
  { label: 'Data', href: '#data' },
]

export default function Navbar() {
  const profile = useActiveProfile()

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
          <li>
            <Link to="/runs" className="text-sm text-muted transition-colors hover:text-ink">
              Resistance
            </Link>
          </li>
          <li>
            {profile ? (
              <Link
                to="/profile"
                className="rounded-full border border-hairline bg-paper px-2.5 py-0.5 text-sm text-ink transition-colors hover:border-[var(--color-accent)] hover:text-accent"
              >
                👤 {profile.name}
              </Link>
            ) : (
              <Link
                to="/profile"
                className="text-sm text-muted transition-colors hover:text-ink"
              >
                Create profile
              </Link>
            )}
          </li>
        </ul>

        <a
          href="#search"
          className="rounded-full border border-ink px-4 py-1.5 text-sm font-medium text-ink transition-colors hover:bg-ink hover:text-paper"
        >
          Search a target
        </a>
      </nav>
    </header>
  )
}
