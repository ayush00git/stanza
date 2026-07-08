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

	pocket := models.Pocket{Center: run.Pockets.Context.MutantPocket.Center}

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

	wtScore, wtPose, err := dockTrack(RunStructurePath(run.ID, "wt"), ligPDBQT, pocket, filepath.Join(tmp, "wt"))
	if err != nil {
		return nil, fmt.Errorf("dock: WT track: %w", err)
	}
	mutScore, mutPose, err := dockTrack(RunStructurePath(run.ID, "mutant"), ligPDBQT, pocket, filepath.Join(tmp, "mutant"))
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

// dockTrack prepares a receptor from a local structure file and docks the already
// prepared ligand into it at the given pocket box, returning the Vina affinity and
// the docked-pose PDB.
func dockTrack(receptorPDB, ligandPDBQT string, pocket models.Pocket, outDir string) (float64, string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, "", err
	}
	receptorPDBQT, err := PrepareReceptor(receptorPDB, outDir)
	if err != nil {
		return 0, "", fmt.Errorf("receptor prep: %w", err)
	}
	res, err := RunVinaDock(receptorPDBQT, ligandPDBQT, pocket, outDir)
	if err != nil {
		return 0, "", err
	}
	pose, _ := os.ReadFile(res.DockedPDB)
	return res.BindingAffinity, string(pose), nil
}
