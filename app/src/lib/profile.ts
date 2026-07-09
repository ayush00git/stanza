/**
 * Client-side "current researcher" store, backed by localStorage. There's no
 * real auth — a profile just lets someone track their run history across
 * reloads, so we persist the active one locally and let the app react to it.
 *
 * Writes dispatch a custom `stanza:profile` window event so components in the
 * same tab update immediately; the native `storage` event covers other tabs.
 */
import { useEffect, useState } from 'react'
import type { Profile } from './api'

const STORAGE_KEY = 'stanza.profile'
const EVENT = 'stanza:profile'

/** Read the active profile from localStorage, or null if absent/malformed. */
export function getActiveProfile(): Profile | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Profile
    // Guard against malformed/partial payloads — a profile must have an id + name.
    if (!parsed || typeof parsed.id !== 'string' || typeof parsed.name !== 'string') {
      return null
    }
    return parsed
  } catch {
    return null
  }
}

/** Persist the active profile and notify same-tab listeners. */
export function setActiveProfile(p: Profile): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(p))
  } catch {
    /* storage full or unavailable — non-fatal */
  }
  window.dispatchEvent(new Event(EVENT))
}

/** Clear the active profile and notify same-tab listeners. */
export function clearActiveProfile(): void {
  try {
    localStorage.removeItem(STORAGE_KEY)
  } catch {
    /* storage unavailable — non-fatal */
  }
  window.dispatchEvent(new Event(EVENT))
}

/**
 * Subscribe to the active profile reactively. Updates on our custom
 * `stanza:profile` event (same-tab writes) and the native `storage` event
 * (writes from other tabs).
 */
export function useActiveProfile(): Profile | null {
  const [profile, setProfile] = useState<Profile | null>(getActiveProfile)

  useEffect(() => {
    const sync = () => setProfile(getActiveProfile())
    window.addEventListener(EVENT, sync)
    window.addEventListener('storage', sync)
    return () => {
      window.removeEventListener(EVENT, sync)
      window.removeEventListener('storage', sync)
    }
  }, [])

  return profile
}
