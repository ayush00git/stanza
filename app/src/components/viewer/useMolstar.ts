import { useRef, useEffect, useState } from 'react'
import { DefaultPluginUISpec } from 'molstar/lib/mol-plugin-ui/spec'
import { createPluginUI } from 'molstar/lib/mol-plugin-ui'
import { PluginConfig } from 'molstar/lib/mol-plugin/config'
import { Color } from 'molstar/lib/mol-util/color'
import { MolScriptBuilder as MS } from 'molstar/lib/mol-script/language/builder'
import { Script } from 'molstar/lib/mol-script/script'
import { StructureSelection, StructureElement } from 'molstar/lib/mol-model/structure'
import { createRoot } from 'react-dom/client'

import 'molstar/lib/mol-plugin-ui/skin/light.scss'

// Canvas background — the site's cool "paper-deep" tone so the viewer sits on
// the page rather than punching a black hole in it.
const VIEWER_BG = 0xf3f6f9
// Hover highlight accent — the assay-blue from the theme (var(--color-accent)).
const HIGHLIGHT = 0x1a56db
// Selection marking color for a highlighted pocket. Applied via Mol*'s selection
// manager, this tints the pocket residues green as an overlay ON TOP of the
// pLDDT coloring (a true highlight) rather than repainting them solid.
const POCKET_SELECT = 0x16a34a
// Docked ligand pose colors: a bright red body with a translucent yellow shell
// around it that reads as a highlight, so the small molecule is easy to spot on
// the much larger receptor.
const POSE_BODY = 0xdc2626
const POSE_HALO = 0xfacc15

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
  /**
   * Structure file format. When omitted it is inferred from the URL extension
   * (.pdb → pdb, else mmcif). Pass explicitly for URLs without an extension —
   * e.g. the runs endpoints serve PDB from /runs/:id/structure/:track.
   */
  format?: 'pdb' | 'mmcif'
  representation?: string
  label?: string
  /**
   * Residues to highlight. Marked with the selection manager (a green overlay
   * that leaves the underlying coloring visible); the camera is NOT moved. Pass
   * an empty/undefined list to clear any existing highlight.
   */
  highlight?: HighlightResidue[]
  /**
   * Raw PDB text of a docked ligand pose to overlay on the protein. Parsed
   * directly (no network fetch to a real URL) and drawn as spheres with a
   * translucent halo. null/undefined removes any existing pose overlay.
   */
  pose?: string | null
  /** Auto-rotate the camera (Mol* trackball spin), for a display / B-roll view. */
  spin?: boolean
}

/**
 * useMolstar — encapsulates the Mol* viewer lifecycle: init, remote structure
 * load, pLDDT confidence coloring, representation switching, pocket highlighting
 * (selection overlay), a docked-pose overlay, and teardown. The structure is
 * fetched by Mol* straight from `structureUrl` into the WebGL scene; nothing is
 * written to the user's filesystem.
 */
export function useMolstar({
  structureUrl,
  format,
  representation = 'cartoon',
  label = '',
  highlight,
  pose,
  spin = false,
}: UseMolstarOptions) {
  const containerRef = useRef<HTMLElementWithRoot | null>(null)
  const pluginRef = useRef<any>(null)
  const initRef = useRef(false)
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Keep the latest highlight/pose in refs so a structure reload or a
  // representation swap (both of which drop the overlays) can re-apply them.
  const highlightRef = useRef<HighlightResidue[] | undefined>(highlight)
  highlightRef.current = highlight
  const poseRef = useRef<string | null | undefined>(pose)
  poseRef.current = pose

  // True while a pocket highlight is active, so click-to-clear behavior doesn't
  // wipe the persistent selection out from under it.
  const pocketActiveRef = useRef(false)
  // The hierarchy StructureRef of the current pose overlay, so it can be
  // skipped by receptor-wide recoloring/restyling.
  const poseStructRef = useRef<any>(null)
  // The transform ref of the receptor structure. Pose removal targets every
  // OTHER structure rather than a single per-pose ref, so a stale or
  // mis-captured pose ref can never leave an old pose stranded in the scene.
  const receptorRefId = useRef<string | undefined>(undefined)
  // Monotonic counter that lets an in-flight setPose notice it has been
  // superseded — e.g. clicking through ranking rows faster than poses build.
  const poseSeqRef = useRef(0)

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
            // Green marking for a selected pocket, blue for hover.
            selectColor: Color(POCKET_SELECT),
            highlightColor: Color(HIGHLIGHT),
          },
        }
        spec.config = spec.config || []
        spec.config.push(
          [PluginConfig.Viewport.ShowExpand, false],
          [PluginConfig.Viewport.ShowSettings, false],
          [PluginConfig.Viewport.ShowAnimation, false],
          [PluginConfig.Viewport.ShowSelectionMode, false],
          // Hide the "Model 1 / 9" trajectory stepper — the docked pose is a
          // multi-model trajectory but we only ever show the best model.
          [PluginConfig.Viewport.ShowTrajectoryControls, false],
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

        // A click normally clears the selection; keep the pocket highlight sticky
        // by only clearing when no pocket is actively highlighted.
        plugin.behaviors.interaction.click.subscribe(() => {
          if (plugin.managers.interactivity && !pocketActiveRef.current) {
            plugin.managers.interactivity.lociSelects.deselectAll()
          }
        })

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

  // Swap representation style on demand, then restore the pocket highlight and
  // pose (rebuilding representations drops the selection marking).
  useEffect(() => {
    const plugin = pluginRef.current
    if (!plugin || !plugin.isInitialized || isLoading) return
    ;(async () => {
      await updateRepresentation(plugin, representation)
      await applyHighlight(plugin, highlightRef.current)
    })()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [representation, isLoading])

  // Mark/unmark the selected pocket's residues when the highlight changes.
  // Serialize the list so identical content doesn't re-run.
  const highlightKey = JSON.stringify(highlight ?? [])
  useEffect(() => {
    const plugin = pluginRef.current
    if (!plugin || !plugin.isInitialized || isLoading) return
    applyHighlight(plugin, highlight)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [highlightKey, isLoading])

  // Add/remove the docked ligand pose overlay when the pose string changes.
  useEffect(() => {
    const plugin = pluginRef.current
    if (!plugin || !plugin.isInitialized || isLoading) return
    setPose(plugin, pose)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pose, isLoading])

  // Auto-spin the camera for a display / B-roll view. Setting the trackball prop is
  // not enough on its own: the spin only advances while the canvas3d animation loop is
  // running, so animate() is kicked when spin turns on. Applied on (re)load and once
  // more shortly after, in case the load's camera reset clobbered it.
  useEffect(() => {
    const apply = () => {
      const plugin = pluginRef.current
      const c3d = plugin?.canvas3d
      if (!plugin?.isInitialized || !c3d) return
      c3d.setProps({
        trackball: {
          animate: spin
            ? { name: 'spin', params: { speed: 0.15, axis: [0, -1, 0] } }
            : { name: 'off', params: {} },
        },
      })
      if (spin) c3d.animate()
    }
    apply()
    const t = setTimeout(apply, 500)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [spin, isLoading])

  const loadStructure = async (plugin: any, url: string) => {
    setIsLoading(true)
    setError(null)
    try {
      await plugin.clear()
      // clear() drops the pose overlay too, so the stale refs are gone.
      poseStructRef.current = null
      receptorRefId.current = undefined

      // Mol* fetches the file itself from the remote URL — no local download.
      const data = await plugin.builders.data.download(
        { url, isBinary: false, label },
        { state: { isGhost: true } },
      )

      // Explicit format wins; otherwise infer from the extension (URLs without
      // one — e.g. the runs structure endpoints — must pass it, or a PDB gets
      // mis-parsed as mmCIF and renders as nothing).
      const fmt = format ?? (url.toLowerCase().endsWith('.pdb') ? 'pdb' : 'mmcif')
      const trajectory = await plugin.builders.structure.parseTrajectory(data, fmt)
      await plugin.builders.structure.hierarchy.applyPreset(trajectory, 'default', {
        representationPreset: 'auto',
      })

      // Right after the preset the receptor is the only structure in the scene;
      // remember its ref so pose overlays (added later) are the ONLY things
      // pose removal ever touches.
      const loaded = plugin.managers.structure.hierarchy.current.structures ?? []
      receptorRefId.current = loaded[loaded.length - 1]?.cell?.transform?.ref

      await applyPlddtColoring(plugin)
      if (representation !== 'cartoon') await updateRepresentation(plugin, representation)

      plugin.managers.camera.reset()

      // A reload wipes the overlays; re-apply the latest highlight and pose.
      await applyHighlight(plugin, highlightRef.current)
      await setPose(plugin, poseRef.current)
    } catch (err: unknown) {
      console.error(`[useMolstar] load failed for ${url}:`, err)
      setError(`Failed to load structure: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setIsLoading(false)
    }
  }

  /** The hierarchy id of the current pose overlay, or undefined. */
  const poseRefId = (): string | undefined => poseStructRef.current?.cell?.transform?.ref

  /**
   * Mark the selected pocket's residues using the selection manager. This tints
   * them green as an overlay on top of the existing (pLDDT) coloring — a real
   * highlight, not a repaint — and does NOT move the camera. Passing an empty or
   * undefined highlight clears any existing selection.
   */
  async function applyHighlight(plugin: any, highlight: HighlightResidue[] | undefined) {
    try {
      const structures = plugin.managers.structure.hierarchy.current.structures
      if (!structures || structures.length === 0) return

      // Always clear the previous selection first.
      plugin.managers.interactivity.lociSelects.deselectAll()

      if (!highlight || highlight.length === 0) {
        pocketActiveRef.current = false
        return
      }

      pocketActiveRef.current = true
      const skip = poseRefId()

      for (const s of structures) {
        // The pocket residues live on the receptor, not the docked pose.
        if (skip && s?.cell?.transform?.ref === skip) continue
        const structure = s.cell?.obj?.data
        if (!structure) continue

        const loci = buildResidueLoci(structure, highlight)
        if (!StructureElement.Loci.isEmpty(loci)) {
          plugin.managers.interactivity.lociSelects.select({ loci })
        }
      }

      // Clear any structure focus so Mol*'s focus representation doesn't dim the
      // rest of the structure — the green selection marking is enough.
      plugin.managers.structure.focus.clear()
    } catch (err) {
      console.warn('[useMolstar] applyHighlight failed:', err)
    }
  }

  /**
   * Remove every structure that isn't the receptor — i.e. any docked-pose
   * overlay(s). Removing by "not the receptor" (rather than a single tracked
   * pose ref) means a stale or wrongly-captured ref can never strand an old
   * pose in the scene, so at most one pose is ever visible.
   */
  async function removePoses(plugin: any) {
    const current = plugin.managers.structure.hierarchy.current.structures ?? []
    const stale = current.filter((s: any) => {
      const ref = s?.cell?.transform?.ref
      return ref && ref !== receptorRefId.current
    })
    if (stale.length > 0) {
      try {
        await plugin.managers.structure.hierarchy.remove(stale)
      } catch (err) {
        console.warn('[useMolstar] pose removal failed:', err)
      }
    }
    poseStructRef.current = null
  }

  /**
   * Overlay a docked ligand pose from raw PDB text on top of the protein, or
   * remove the current one when `poseString` is empty. The pose is loaded via a
   * Blob object URL (no network fetch) and drawn as red spheres wrapped in a
   * translucent yellow halo. Any previous pose is removed first, so exactly one
   * pose is ever shown. The camera is left where it is.
   */
  async function setPose(plugin: any, poseString: string | null | undefined) {
    // Claim this call's turn. A later setPose bumps the counter, so an older
    // one that's still awaiting an async builder can detect it's been
    // superseded and clean up after itself instead of leaving two poses.
    const seq = ++poseSeqRef.current

    // Drop any existing pose first so at most one is in the scene.
    await removePoses(plugin)

    if (!poseString || seq !== poseSeqRef.current) return

    const url = URL.createObjectURL(new Blob([poseString], { type: 'text/plain' }))
    try {
      const data = await plugin.builders.data.download(
        { url, isBinary: false, label: 'docked-pose' },
        { state: { isGhost: false } },
      )
      const trajectory = await plugin.builders.structure.parseTrajectory(data, 'pdb')
      // Vina writes up to 9 ranked binding modes into the pose PDB; render only
      // the best one (model index 0 = mode 1, the lowest binding energy).
      const model = await plugin.builders.structure.createModel(trajectory, {
        modelIndex: 0,
      })
      const struct = await plugin.builders.structure.createStructure(model, {
        name: 'model',
        params: {},
      })

      const comp = await plugin.builders.structure.tryCreateComponentStatic(struct, 'all')
      if (comp) {
        // Molecule body — red spheres.
        await plugin.builders.structure.representation.addRepresentation(comp, {
          type: 'spacefill',
          color: 'uniform',
          colorParams: { value: Color(POSE_BODY) },
        })
        // A slightly larger translucent yellow shell that reads as a highlight.
        await plugin.builders.structure.representation.addRepresentation(comp, {
          type: 'spacefill',
          typeParams: { alpha: 0.35, sizeFactor: 1.25 },
          color: 'uniform',
          colorParams: { value: Color(POSE_HALO) },
        })
      }

      // Sync the hierarchy so the new structure is visible to the managers.
      plugin.managers.structure.hierarchy.sync(true)

      // A newer setPose landed while this one was building its pose — discard
      // what we just added so only the newest pose survives.
      if (seq !== poseSeqRef.current) {
        await removePoses(plugin)
        return
      }

      // Record the newly added structure as the current pose overlay.
      const structures = plugin.managers.structure.hierarchy.current.structures
      poseStructRef.current = structures[structures.length - 1] ?? null
    } catch (err) {
      console.warn('[useMolstar] setPose failed:', err)
    } finally {
      URL.revokeObjectURL(url)
    }
  }

  /** Replace the representation type on receptor components, then re-color. */
  async function updateRepresentation(plugin: any, type: string) {
    try {
      const { structures } = plugin.managers.structure.hierarchy.current
      if (!structures || structures.length === 0) return
      const mgr = plugin.managers.structure.component
      const skip = poseRefId()

      for (const s of structures) {
        // Leave the docked pose alone — it keeps its red spheres + yellow halo.
        if (skip && s?.cell?.transform?.ref === skip) continue
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
   * list to get the familiar blue(high) → orange(low) scale. The docked pose is
   * excluded so it keeps its red/yellow coloring.
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
    const skip = poseRefId()

    for (const s of structures) {
      if (skip && s?.cell?.transform?.ref === skip) continue
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
          if (skip && s?.cell?.transform?.ref === skip) continue
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

  return { containerRef, isLoading, error }
}

/**
 * Build a Mol* element Loci for the given (chain, residue-number) pairs.
 *
 * Residue-indexing assumption: fpocket's `residue_indices` are PDB residue
 * numbers, which equal the mmCIF `auth_seq_id` (1-based). We use them verbatim
 * — no off-by-one shift is applied. Chain ids map to `auth_asym_id`.
 *
 * We group the residue numbers per chain and OR the per-chain groups together.
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
