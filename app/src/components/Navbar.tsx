import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { CircleUser, FlaskConical, Menu, X } from 'lucide-react'
import { useActiveProfile } from '../lib/profile'

// The CTA already points at #search, so it isn't repeated here.
const links = [
  { label: 'Pipeline', href: '#pipeline' },
  { label: 'Scoring', href: '#selectivity' },
  { label: 'Covalent', href: '#covalent' },
  { label: 'Stack', href: '#data' },
]

/** True once the page has scrolled off the top, so the bar can earn its border. */
function useScrolled() {
  const [scrolled, setScrolled] = useState(false)
  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 8)
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])
  return scrolled
}

/** The id of the section currently occupying the top of the viewport. */
function useActiveSection(ids: string[]) {
  const [active, setActive] = useState<string | null>(null)

  useEffect(() => {
    const sections = ids
      .map((id) => document.getElementById(id))
      .filter((el): el is HTMLElement => el !== null)
    if (!sections.length) return

    const observer = new IntersectionObserver(
      (entries) => {
        // Whichever tracked section is intersecting nearest the top wins.
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        if (visible[0]) setActive(visible[0].target.id)
      },
      // Only the band just under the sticky bar counts as "here".
      { rootMargin: '-72px 0px -70% 0px', threshold: 0 },
    )
    sections.forEach((s) => observer.observe(s))
    return () => observer.disconnect()
  }, [ids])

  return active
}

const sectionIds = links.map((l) => l.href.slice(1))

export default function Navbar() {
  const profile = useActiveProfile()
  const scrolled = useScrolled()
  const active = useActiveSection(sectionIds)
  const [open, setOpen] = useState(false)

  // A hash link on a closed menu should never leave the panel hanging open.
  const close = () => setOpen(false)

  return (
    <header
      className={`sticky top-0 z-50 transition-colors ${
        scrolled
          ? 'border-b border-hairline bg-paper/85 backdrop-blur-sm'
          : 'border-b border-transparent bg-paper'
      }`}
    >
      <nav className="mx-auto flex max-w-5xl items-center justify-between gap-6 px-6 py-4">
        <a
          href="#top"
          className="font-display text-xl font-medium tracking-[-0.02em] text-ink"
        >
          Stanza
          <span className="text-accent">.</span>
        </a>

        <ul className="hidden items-center gap-7 md:flex">
          {links.map((link) => {
            const isActive = active === link.href.slice(1)
            return (
              <li key={link.href}>
                <a
                  href={link.href}
                  aria-current={isActive ? 'true' : undefined}
                  className={`relative py-1 text-sm transition-colors ${
                    isActive ? 'text-ink' : 'text-muted hover:text-ink'
                  }`}
                >
                  {link.label}
                  <span
                    className={`absolute inset-x-0 -bottom-0.5 h-px origin-left bg-accent transition-transform duration-300 ${
                      isActive ? 'scale-x-100' : 'scale-x-0'
                    }`}
                  />
                </a>
              </li>
            )
          })}
        </ul>

        <div className="flex items-center gap-2">
          <Link
            to="/runs"
            className="hidden items-center gap-1.5 rounded-full px-3 py-1.5 text-sm text-muted transition-colors hover:text-ink sm:inline-flex"
          >
            <FlaskConical className="h-4 w-4" aria-hidden />
            Runs
          </Link>

          <Link
            to="/profile"
            aria-label={profile ? `Profile — ${profile.name}` : 'Create a profile'}
            className="inline-flex items-center gap-2 rounded-full border border-hairline bg-paper px-2.5 py-1.5 text-sm text-muted transition-colors hover:border-accent hover:text-accent"
          >
            <CircleUser className="h-4 w-4 shrink-0" aria-hidden />
            <span className="hidden max-w-[9rem] truncate sm:inline">
              {profile ? profile.name : 'Create profile'}
            </span>
          </Link>

          <a
            href="#search"
            className="hidden shrink-0 rounded-full border border-ink px-4 py-1.5 text-sm font-medium text-ink transition-colors hover:bg-ink hover:text-paper sm:inline-block"
          >
            Search a target
          </a>

          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            aria-expanded={open}
            aria-controls="nav-menu"
            aria-label={open ? 'Close menu' : 'Open menu'}
            className="-mr-1 inline-flex items-center rounded-full p-2 text-ink transition-colors hover:bg-paper-deep md:hidden"
          >
            {open ? (
              <X className="h-5 w-5" aria-hidden />
            ) : (
              <Menu className="h-5 w-5" aria-hidden />
            )}
          </button>
        </div>
      </nav>

      {open && (
        <div
          id="nav-menu"
          className="border-t border-hairline bg-paper px-6 pb-5 pt-2 md:hidden"
        >
          <ul className="flex flex-col">
            {links.map((link) => (
              <li key={link.href}>
                <a
                  href={link.href}
                  onClick={close}
                  className="block border-b border-hairline py-3 text-sm text-muted transition-colors hover:text-ink"
                >
                  {link.label}
                </a>
              </li>
            ))}
            <li>
              <Link
                to="/runs"
                onClick={close}
                className="flex items-center gap-2 border-b border-hairline py-3 text-sm text-muted transition-colors hover:text-ink"
              >
                <FlaskConical className="h-4 w-4" aria-hidden />
                Runs
              </Link>
            </li>
          </ul>

          <a
            href="#search"
            onClick={close}
            className="mt-5 block rounded-full bg-ink px-5 py-2.5 text-center text-sm font-medium text-paper"
          >
            Search a target
          </a>
        </div>
      )}
    </header>
  )
}
