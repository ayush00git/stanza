package services

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ayush00git/stanza/models"
)

const (
    dockTimeout = 10 * 60 // seconds
    // ligprepScript builds the ligand's 3D conformer, resolved relative to the
    // server's working directory (the repo root, like scripts/mutate.py).
    ligprepScript = "scripts/ligprep.py"
    // DefaultLigandSeed fixes the ETKDG conformer search. obabel --gen3d, which this
    // replaced, was unseeded: the same SMILES yielded a different 3D structure every
    // invocation, so a molecule re-docked against the same receptor moved. Vina's
    // --seed pins the search but not the ligand it searches from.
    DefaultLigandSeed = 42
)

// DockResult holds docked ligand conformations
type DockResult struct {
    JobID           string
    PocketID        int
    BindingAffinity float64
    DockedPDBQT     string
    DockedPDB       string
    Status          string
    Error           string
}

// SMILESTo3D generates 3D coordinates from SMILES using OpenBabel
func SMILESTo3D(smiles string, outDir string) (string, error) {
    outPath := filepath.Join(outDir, "ligand_3D.sdf")
    cmd := exec.Command("python3", ligprepScript,
        "--smiles", smiles,
        "--out", outPath,
        "--seed", strconv.Itoa(DefaultLigandSeed),
    )
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("SMILESTo3D: %w (stderr: %s)", err, stderr.String())
    }
    return outPath, nil
}

// PrepareReceptor converts receptor PDB to PDBQT using OpenBabel, then strips
// any lines that Vina's rigid-receptor parser does not recognise (e.g. HEADER,
// TITLE, COMPND …).
func PrepareReceptor(pdbPath, outDir string) (string, error) {
    outPath := filepath.Join(outDir, "receptor.pdbqt")
    // Use -xr (rigid) to prevent ROOT/ENDROOT tags which Vina rejects for receptors
    cmd := exec.Command("obabel", pdbPath, "-O", outPath, "-xr")
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("PrepareReceptor: %w (stderr: %s)", err, stderr.String())
    }

    // Post-process: Vina only accepts ATOM, HETATM, REMARK, TER, END, MODEL,
    // ENDMDL in a rigid receptor PDBQT. Strip everything else (HEADER, TITLE,
    // COMPND, SOURCE, KEYWDS, EXPDTA, AUTHOR, REVDAT, JRNL, SEQRES, etc.).
    if err := stripNonPDBQTLines(outPath); err != nil {
        return "", fmt.Errorf("PrepareReceptor: failed to clean PDBQT: %w", err)
    }

    return outPath, nil
}

// PrepareLigand converts the ligand's 3D structure → PDBQT, then strips any lines
// that Vina's ligand parser does not recognise (e.g. COMPND, AUTHOR …). The input is
// the SDF written by SMILESTo3D, whose explicit bond block spares OpenBabel from
// re-perceiving bond orders off the coordinates — a guess it gets wrong on fused
// heteroaromatic scaffolds.
func PrepareLigand(ligandPath, outDir string) (string, error) {
    outPath := filepath.Join(outDir, "ligand.pdbqt")
    // Use -ph 7.4 to protonate and -xh to add hydrogens for the ligand
    cmd := exec.Command("obabel", ligandPath, "-O", outPath, "-ph", "7.4", "-xh")
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("PrepareLigand: %w (stderr: %s)", err, stderr.String())
    }

    // Post-process: strip lines Vina doesn't accept in a ligand PDBQT.
    if err := stripNonPDBQTLigandLines(outPath); err != nil {
        return "", fmt.Errorf("PrepareLigand: failed to clean PDBQT: %w", err)
    }

    return outPath, nil
}

// Docking-box geometry. A box far larger than its pocket enlarges the search space
// without adding signal: at a fixed exhaustiveness Vina samples a bigger volume
// less densely, so the best pose it happens to find drifts from seed to seed.
// Measured on the KRAS G12C switch-II pocket, a 25 Å cube gave a seed-to-seed
// selectivity spread of sd 0.039 kcal/mol against sd 0.004 for a pocket-sized 20 Å
// box — an order of magnitude, on a margin that is itself only ~0.15. Size the box
// to the pocket rather than to a constant.
const (
	// boxPadding (Å) is the room added around a pocket's own diameter so a ligand
	// can rotate freely inside the box.
	boxPadding = 8.0
	// minBoxSize (Å) still admits a drug-sized ligand in any orientation.
	minBoxSize = 20.0
	// maxBoxSize (Å) caps the search volume for large interface pockets.
	maxBoxSize = 26.0
	// fallbackBoxSize (Å) is used when a pocket carries no volume.
	fallbackBoxSize = 22.0
	// DefaultDockSeed fixes Vina's RNG so a molecule re-docked against the same
	// receptor yields the same score. Without it, replicate scores drift by a few
	// hundredths — the same magnitude as the selectivity margin being measured.
	DefaultDockSeed = 42
)

// VinaOptions tune one docking run. A zero BoxSize derives the box from the
// pocket's volume; a zero Seed uses DefaultDockSeed; a zero NumModes uses Vina's
// default. More modes let the covalent-reach scan inspect lower-ranked poses whose
// warhead points at the thiol without changing the best (mode-1) score.
type VinaOptions struct {
	Exhaustiveness int
	CPU            int
	Seed           int
	BoxSize        float64
	NumModes       int
}

// boxSizeFor returns the cube edge (Å) enclosing a pocket plus padding, clamped to
// [minBoxSize, maxBoxSize]. The pocket is treated as a sphere of equal volume.
func boxSizeFor(p models.Pocket) float64 {
	if p.Volume <= 0 {
		return fallbackBoxSize
	}
	radius := math.Cbrt(3 * p.Volume / (4 * math.Pi))
	return math.Min(math.Max(2*radius+boxPadding, minBoxSize), maxBoxSize)
}

// RunVinaDock docks ligand into receptor using Vina and returns best pose.
// exhaustiveness/cpu are tunable: high exhaustiveness for a one-off dock, lower
// for screening many molecules in a loop.
func RunVinaDock(receptorPDBQT, ligandPDBQT string, pocket models.Pocket, opts VinaOptions, outDir string) (DockResult, error) {
    outPDBQT := filepath.Join(outDir, "docked.pdbqt")
    outPDB := filepath.Join(outDir, "docked.pdb")

    size := opts.BoxSize
    if size <= 0 {
        size = boxSizeFor(pocket)
    }
    seed := opts.Seed
    if seed == 0 {
        seed = DefaultDockSeed
    }
    args := []string{
        "--receptor", receptorPDBQT,
        "--ligand", ligandPDBQT,
        "--center_x", fmt.Sprintf("%.3f", pocket.Center[0]),
        "--center_y", fmt.Sprintf("%.3f", pocket.Center[1]),
        "--center_z", fmt.Sprintf("%.3f", pocket.Center[2]),
        "--size_x", fmt.Sprintf("%.3f", size),
        "--size_y", fmt.Sprintf("%.3f", size),
        "--size_z", fmt.Sprintf("%.3f", size),
        "--exhaustiveness", fmt.Sprint(opts.Exhaustiveness),
        "--cpu", fmt.Sprint(opts.CPU),
        "--seed", fmt.Sprint(seed),
        "--out", outPDBQT,
    }
    if opts.NumModes > 0 {
        args = append(args, "--num_modes", fmt.Sprint(opts.NumModes))
    }
    cmd := exec.Command("vina", args...)
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        return DockResult{}, fmt.Errorf("RunVinaDock: %w (stderr: %s)", err, stderr.String())
    }

    // Convert docked PDBQT to PDB for visualization
    cmd2 := exec.Command("obabel", outPDBQT, "-O", outPDB)
    var stderr2 bytes.Buffer
    cmd2.Stderr = &stderr2
    if err := cmd2.Run(); err != nil {
        return DockResult{}, fmt.Errorf("PDBQT to PDB: %w (stderr: %s)", err, stderr2.String())
    }

    // Parse binding affinity from Vina output
    affinity := parseVinaAffinity(stdout.String())

    return DockResult{
        PocketID:        pocket.PocketID,
        DockedPDBQT:     outPDBQT,
        DockedPDB:       outPDB,
        BindingAffinity: affinity,
        Status:          "done",
    }, nil
}

// parseVinaAffinity extracts first docking pose affinity
func parseVinaAffinity(out string) float64 {
    lines := strings.Split(out, "\n")
    for _, l := range lines {
        l = strings.TrimSpace(l)
        if strings.HasPrefix(l, "1") { // first mode
            fields := strings.Fields(l)
            if len(fields) >= 2 {
                if aff, err := strconv.ParseFloat(fields[1], 64); err == nil {
                    return aff
                }
            }
        }
    }
    return 0
}

// stripNonPDBQTLines rewrites a PDBQT file in-place, keeping only lines whose
// record type is recognised by Vina's rigid-receptor parser. It also extracts
// only the first MODEL (Vina rejects multi-MODEL rigid receptors) and removes
// the MODEL/ENDMDL wrapper lines themselves.
func stripNonPDBQTLines(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }

    var kept []string
    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        line := scanner.Text()

        // Stop after the first model ends — discard all subsequent models.
        if strings.HasPrefix(line, "ENDMDL") {
            break
        }

        // Skip MODEL tags — Vina doesn't want them for rigid receptors.
        if strings.HasPrefix(line, "MODEL") {
            continue
        }

        if isVinaSafeRecord(line) {
            kept = append(kept, line)
        }
    }
    if err := scanner.Err(); err != nil {
        f.Close()
        return err
    }
    f.Close()

    // Ensure the file ends with an END record
    if len(kept) == 0 || !strings.HasPrefix(kept[len(kept)-1], "END") {
        kept = append(kept, "END")
    }

    return os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0644)
}

// isVinaSafeRecord returns true if the line starts with a record type that
// Vina accepts in a rigid receptor PDBQT file.
func isVinaSafeRecord(line string) bool {
    if len(line) == 0 {
        return true // blank lines are harmless
    }
    // Fast prefix check against the small set of allowed tags.
    for _, prefix := range []string{
        "ATOM", "HETATM", "REMARK", "TER",
    } {
        if strings.HasPrefix(line, prefix) {
            return true
        }
    }
    return false
}

// stripNonPDBQTLigandLines rewrites a ligand PDBQT file in-place, keeping only
// lines whose record type is recognised by Vina's ligand parser.
func stripNonPDBQTLigandLines(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }

    var kept []string
    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        line := scanner.Text()
        if isVinaLigandSafeRecord(line) {
            kept = append(kept, line)
        }
    }
    if err := scanner.Err(); err != nil {
        f.Close()
        return err
    }
    f.Close()

    return os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0644)
}

// isVinaLigandSafeRecord returns true if the line starts with a record type
// that Vina accepts in a ligand PDBQT file.
func isVinaLigandSafeRecord(line string) bool {
    if len(line) == 0 {
        return true
    }
    for _, prefix := range []string{
        "ATOM", "HETATM", "REMARK", "TER", "END",
        "ROOT", "ENDROOT", "BRANCH", "ENDBRANCH", "TORSDOF",
    } {
        if strings.HasPrefix(line, prefix) {
            return true
        }
    }
    return false
}