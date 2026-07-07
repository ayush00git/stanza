import { useRef, useEffect, useState } from 'react'
import { DefaultPluginUISpec } from 'molstar/lib/mol-plugin-ui/spec'
import { createPluginUI } from 'molstar/lib/mol-plugin-ui'
import { PluginConfig } from 'molstar/lib/mol-plugin/config'
import { Color } from 'molstar/lib/mol-util/color'
import {
  setStructureOverpaint,
  clearStructureOverpaint,
} from 'molstar/lib/mol-plugin-state/helpers/structure-overpaint'
import { MolScriptBuilder as MS } from 'molstar/lib/mol-script/language/builder'
import { Script } from 'molstar/lib/mol-script/script'
import { StructureSelection, StructureElement } from 'molstar/lib/mol-model/structure'
import { createRoot } from 'react-dom/client'

import 'molstar/lib/mol-plugin-ui/skin/light.scss'

// Canvas background — the site's cool "paper-deep" tone so the viewer sits on
// the page rather than punching a black hole in it.
const VIEWER_BG = 0xf3f6f9
// Hover/selection accent — the assay-blue from the theme (var(--color-accent)).
const HIGHLIGHT = 0x1a56db
// Overpaint color used to mark a selected pocket's residues — same accent blue.
const ACCENT = 0x1a56db

/** One residue to spotlight: its chain id and residue sequence number. */
export interface HighlightResidue {
  chain: string
  index: number
}

interface HTMLElementWithRoot extends HTMLDivElement {
  __molstarRoot?: ReturnType<typeof createRoot>
}

interface UseMolstarOptions {
  /** Remote .cif/.pdb URL. Loaded by Mol* directly — never downloaded to disk. */
  structureUrl?: string
  representation?: string
  label?: string
  /**
   * Residues to highlight (overpaint in the accent color + focus the camera).
   * Pass an empty/undefined list to clear any existing highlight.
   */
  highlight?: HighlightResidue[]
}

/**
 * useMolstar — encapsulates the Mol* viewer lifecycle: init, remote structure
 * load, pLDDT confidence coloring, representation switching, and teardown.
 * The structure is fetched by Mol* straight from `structureUrl` into the WebGL
 * scene; nothing is written to the user's filesystem.
 */
export function useMolstar({
  structureUrl,
  representation = 'cartoon',
  label = '',
  highlight,
}: UseMolstarOptions) {
  const containerRef = useRef<HTMLElementWithRoot | null>(null)
  const pluginRef = useRef<any>(null)
  const initRef = useRef(false)
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Keep the latest highlight in a ref so representation swaps (which rebuild
  // the representations and would wipe the overpaint) can re-apply it.
  const highlightRef = useRef<HighlightResidue[] | undefined>(highlight)
  highlightRef.current = highlight

  // Initialize the plugin once, on mount.
  useEffect(() => {
    if (!containerRef.current || initRef.current) return
    initRef.current = true
    let disposed = false

    const init = async () => {
      try {
        const spec = DefaultPluginUISpec()

        // Minimal chrome: no side panels, just the 3D canvas + hover tooltips.
        spec.layout = {
          initial: {
            isExpanded: false,
            showControls: false,
            regionState: { bottom: 'hidden', left: 'hidden', right: 'hidden', top: 'hidden' },
          },
        }
        spec.canvas3d = {
          renderer: {
            backgroundColor: Color(VIEWER_BG),
            highlightColor: Color(HIGHLIGHT),
          },
        }
        spec.config = spec.config || []
        spec.config.push(
          [PluginConfig.Viewport.ShowExpand, false],
          [PluginConfig.Viewport.ShowSettings, false],
          [PluginConfig.Viewport.ShowAnimation, false],
          [PluginConfig.Viewport.ShowSelectionMode, false],
        )

        const plugin = await createPluginUI({
          target: containerRef.current as HTMLElement,
          spec,
          render: (component: any, target: any) => {
            const el = target as HTMLElementWithRoot
            let root = el.__molstarRoot
            if (!root) {
              root = createRoot(el)
              el.__molstarRoot = root
            }
            root.render(component)
          },
        })

        if (disposed) {
          plugin.dispose()
          return
        }
        pluginRef.current = plugin

        if (structureUrl) await loadStructure(plugin, structureUrl)
      } catch (err: unknown) {
        console.error('[useMolstar] init failed:', err)
        if (!disposed) {
          setError(`Viewer init failed: ${err instanceof Error ? err.message : String(err)}`)
        }
      }
    }

    init()

    return () => {
      disposed = true
      if (pluginRef.current) {
        pluginRef.current.dispose()
        pluginRef.current = null
      }
      if (containerRef.current?.__molstarRoot) {
        containerRef.current.__molstarRoot.unmount()
        delete containerRef.current.__molstarRoot
      }
      initRef.current = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Reload when the URL changes after init.
  useEffect(() => {
    const plugin = pluginRef.current
    if (!plugin || !plugin.isInitialized || !structureUrl) return
    loadStructure(plugin, structureUrl)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [structureUrl])

  // Swap representation style on demand, then restore the pocket highlight
  // (rebuilding representations clears any overpaint).
  useEffect(() => {
    const plugin = pluginRef.current
    if (!plugin || !plugin.isInitialized || isLoading) return
    ;(async () => {
      await updateRepresentation(plugin, representation)
      await applyHighlight(plugin, highlightRef.current)
    })()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [representation, isLoading])

  // Highlight (overpaint + camera focus) the selected pocket's residues in the
  // correct viewer. Serialize the list so identical content doesn't re-run.
  const highlightKey = JSON.stringify(highlight ?? [])
  useEffect(() => {
    const plugin = pluginRef.current
    if (!plugin || !plugin.isInitialized || isLoading) return
    applyHighlight(plugin, highlight)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [highlightKey, isLoading])

  const loadStructure = async (plugin: any, url: string) => {
    setIsLoading(true)
    setError(null)
    try {
      await plugin.clear()

      // Mol* fetches the file itself from the remote URL — no local download.
      const data = await plugin.builders.data.download(
        { url, isBinary: false, label },
        { state: { isGhost: true } },
      )

      const format = url.toLowerCase().endsWith('.pdb') ? 'pdb' : 'mmcif'
      const trajectory = await plugin.builders.structure.parseTrajectory(data, format)
      await plugin.builders.structure.hierarchy.applyPreset(trajectory, 'default', {
        representationPreset: 'auto',
      })

      await applyPlddtColoring(plugin)
      if (representation !== 'cartoon') await updateRepresentation(plugin, representation)

      plugin.managers.camera.reset()
    } catch (err: unknown) {
      console.error(`[useMolstar] load failed for ${url}:`, err)
      setError(`Failed to load structure: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setIsLoading(false)
    }
  }

  return { containerRef, isLoading, error }
}

/**
 * Build a Mol* element Loci for the given (chain, residue-number) pairs.
 *
 * fpocket reports PDB residue numbers, which map to mmCIF `auth_seq_id`, and
 * chain ids that map to `auth_asym_id`. For AlphaFold models these coincide
 * with the 1-based sequence position and the single-letter chain. We group the
 * residue numbers per chain and OR the per-chain groups together.
 */
function buildResidueLoci(structure: any, highlight: HighlightResidue[]) {
  const byChain = new Map<string, number[]>()
  for (const { chain, index } of highlight) {
    const arr = byChain.get(chain)
    if (arr) arr.push(index)
    else byChain.set(chain, [index])
  }

  const groups = [...byChain.entries()].map(([chain, indices]) =>
    MS.struct.generator.atomGroups({
      'chain-test': MS.core.rel.eq([MS.ammp('auth_asym_id'), chain]),
      'residue-test': MS.core.set.has([MS.set(...indices), MS.ammp('auth_seq_id')]),
      'group-by': MS.struct.atomProperty.macromolecular.residueKey(),
    }),
  )

  const expr = groups.length === 1 ? groups[0] : MS.struct.combinator.merge(groups)
  const sel = Script.getStructureSelection(expr, structure)
  return StructureSelection.toLociWithSourceUnits(sel)
}

/**
 * Overpaint the selected pocket's residues in the accent color and focus the
 * camera on them. Passing an empty highlight clears any existing overpaint.
 */
async function applyHighlight(plugin: any, highlight: HighlightResidue[] | undefined) {
  try {
    const structures = plugin.managers.structure.hierarchy.current.structures
    if (!structures || structures.length === 0) return

    const components: any[] = []
    for (const s of structures) {
      if (s.components) components.push(...s.components)
    }
    if (components.length === 0) return

    // Always clear the previous highlight first.
    await clearStructureOverpaint(plugin, components)
    plugin.managers.interactivity.lociHighlights.clearHighlights()

    if (!highlight || highlight.length === 0) return

    await setStructureOverpaint(
      plugin,
      components,
      Color(ACCENT),
      async (structure: any) => buildResidueLoci(structure, highlight),
    )

    // Focus the camera on the pocket in this viewer.
    const root = structures[0].cell?.obj?.data
    if (root) {
      const loci = buildResidueLoci(root, highlight)
      if (!StructureElement.Loci.isEmpty(loci)) {
        plugin.managers.camera.focusLoci(loci)
        plugin.managers.interactivity.lociHighlights.highlightOnly({ loci })
      }
    }
  } catch (err) {
    console.warn('[useMolstar] applyHighlight failed:', err)
  }
}

/** Replace the representation type on every component, then re-apply pLDDT color. */
async function updateRepresentation(plugin: any, type: string) {
  try {
    const { structures } = plugin.managers.structure.hierarchy.current
    if (!structures || structures.length === 0) return
    const mgr = plugin.managers.structure.component

    for (const s of structures) {
      if (!s.components) continue
      for (const comp of s.components) {
        if (!comp.representations || comp.representations.length === 0) continue
        await mgr.removeRepresentations([comp])
        await mgr.addRepresentation([comp], type)
      }
    }
    await applyPlddtColoring(plugin)
  } catch (err) {
    console.warn('[useMolstar] updateRepresentation failed:', err)
  }
}

/**
 * Color by AlphaFold pLDDT confidence. Mol*'s "uncertainty" theme reads the
 * per-residue B-factor column (which AlphaFold CIFs use for pLDDT), but its
 * default gradient runs the wrong way for confidence, so we reverse the color
 * list to get the familiar blue(high) → orange(low) scale.
 */
async function applyPlddtColoring(plugin: any) {
  const themeNames = ['uncertainty', 'plddt-confidence', 'b-factor']
  const registry = plugin.representation.structure.themes.colorThemeRegistry
  const available = registry._list || []

  let themeName: string | null = null
  for (const name of themeNames) {
    if (available.some((t: any) => t.name === name)) {
      themeName = name
      break
    }
  }
  if (!themeName) return

  const structures = plugin.managers.structure.hierarchy.current.structures
  if (!structures) return

  for (const s of structures) {
    if (!s.components) continue
    const valid = s.components.filter((c: any) => c && c.representations)
    if (valid.length > 0) {
      try {
        await plugin.managers.structure.component.updateRepresentationsTheme(valid, {
          color: themeName,
        })
      } catch (e) {
        console.warn('[useMolstar] failed to apply pLDDT theme:', e)
      }
    }
  }

  // Reverse the "uncertainty" gradient so high confidence reads blue, low orange.
  if (themeName === 'uncertainty') {
    try {
      const update = plugin.state.data.build()
      let patched = false
      for (const s of structures) {
        if (!s.components) continue
        for (const comp of s.components) {
          if (!comp.representations) continue
          for (const repr of comp.representations) {
            const cell = repr.cell
            if (cell?.transform?.params?.colorTheme?.name !== 'uncertainty') continue
            update.to(cell).update((old: any) => {
              if (old.colorTheme?.params?.list?.colors) {
                old.colorTheme.params.list.colors = [...old.colorTheme.params.list.colors].reverse()
              }
            })
            patched = true
          }
        }
      }
      if (patched) await update.commit()
    } catch (e) {
      console.warn('[useMolstar] failed to patch pLDDT color list:', e)
    }
  }
}
