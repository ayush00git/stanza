package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ayush00git/stanza/models"
)

// DockLigandDualTrack is Stage 4. It docks one SMILES ligand into BOTH tracks of a
// run — the WT structure and the mutant structure — using the resistance pocket as
// the docking box, and returns the paired affinities, the selectivity margin
// (wt_score - mutant_score), and both poses. The box center is shared: the two
// structures differ by a single side chain, so selectivity comes from the receptor
// (the mutated residue), not from moving the box.
func DockLigandDualTrack(ctx context.Context, run *models.Run, smiles string) (*models.LigandDock, error) {
	smiles = strings.TrimSpace(smiles)
	if smiles == "" {
		return nil, fmt.Errorf("dock: empty ligand SMILES")
	}
	if run.Mutagenesis == nil {
		return nil, fmt.Errorf("dock: run has no structures (run Stage-2 mutagenesis first)")
	}
	if run.Pockets == nil || run.Pockets.Context == nil {
		return nil, fmt.Errorf("dock: run has no resistance pocket (run Stage-3 analysis first)")
	}

	// Volume is carried so both tracks derive the same pocket-sized docking box.
	pocket := models.Pocket{
		Center: run.Pockets.Context.MutantPocket.Center,
		Volume: run.Pockets.Context.MutantPocket.Volume,
	}

	tmp, err := os.MkdirTemp("", "dualdock-")
	if err != nil {
		return nil, fmt.Errorf("dock: create workspace: %w", err)
	}
	defer os.RemoveAll(tmp)

	// Prepare the ligand once; both docks reuse it.
	ligPDB, err := SMILESTo3D(smiles, tmp)
	if err != nil {
		return nil, fmt.Errorf("dock: ligand 3D generation: %w", err)
	}
	ligPDBQT, err := PrepareLigand(ligPDB, tmp)
	if err != nil {
		return nil, fmt.Errorf("dock: ligand prep: %w", err)
	}

	wtScore, wtPose, err := dockTrack(run.ID, "wt", ligPDBQT, pocket, filepath.Join(tmp, "wt"))
	if err != nil {
		return nil, fmt.Errorf("dock: WT track: %w", err)
	}
	mutScore, mutPose, err := dockTrack(run.ID, "mutant", ligPDBQT, pocket, filepath.Join(tmp, "mutant"))
	if err != nil {
		return nil, fmt.Errorf("dock: mutant track: %w", err)
	}

	_ = ctx // reserved for cancellation once the docking CLIs accept a context
	return &models.LigandDock{
		SMILES:        smiles,
		WTScore:       round2(wtScore),
		MutantScore:   round2(mutScore),
		Selectivity:   round2(wtScore - mutScore),
		WTPosePDB:     wtPose,
		MutantPosePDB: mutPose,
	}, nil
}

// Screening docking parameters — lower exhaustiveness than a one-off dock, since
// the generation loop scores many molecules and only needs reliable relative
// ranking, not final-quality poses.
const (
	screenExhaustiveness = 8
	screenCPU            = 2
)

// screenVinaOptions are shared by both tracks: identical box and identical seed, so
// a WT/mutant score difference can only come from the receptor.
var screenVinaOptions = VinaOptions{
	Exhaustiveness: screenExhaustiveness,
	CPU:            screenCPU,
	Seed:           DefaultDockSeed,
}

// dockTrack docks the prepared ligand into a run's structure for one track, reusing
// the run's cached receptor PDBQT (prepared once via ensureReceptorPDBQT), and
// returns the Vina affinity and the docked-pose PDB.
func dockTrack(runID, track, ligandPDBQT string, pocket models.Pocket, outDir string) (float64, string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, "", err
	}
	receptorPDBQT, err := ensureReceptorPDBQT(runID, track)
	if err != nil {
		return 0, "", fmt.Errorf("receptor prep: %w", err)
	}
	res, err := RunVinaDock(receptorPDBQT, ligandPDBQT, pocket, screenVinaOptions, outDir)
	if err != nil {
		return 0, "", err
	}
	pose, _ := os.ReadFile(res.DockedPDB)
	return res.BindingAffinity, string(pose), nil
}

// ensureReceptorPDBQT prepares a run's receptor PDBQT for a track once and caches it
// under the run's structure directory, so repeated docks against the same run don't
// re-run the (identical) receptor prep. Concurrency-safe: the final file is written
// via a temp file + atomic rename.
func ensureReceptorPDBQT(runID, track string) (string, error) {
	dst := filepath.Join(RunStructureDir(runID), track+"_receptor.pdbqt")
	if info, err := os.Stat(dst); err == nil && info.Size() > 0 {
		return dst, nil
	}
	// PrepareReceptor writes "receptor.pdbqt" into a scratch dir; move it into place.
	scratch, err := os.MkdirTemp("", "recprep-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)
	prepared, err := PrepareReceptor(RunStructurePath(runID, track), scratch)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(prepared)
	if err != nil {
		return "", err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil { // atomic on the same filesystem
		return "", err
	}
	return dst, nil
}
