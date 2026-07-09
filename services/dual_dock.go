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

	wtScore, wtPose, _, err := dockTrack(run.ID, "wt", ligPDBQT, pocket, filepath.Join(tmp, "wt"))
	if err != nil {
		return nil, fmt.Errorf("dock: WT track: %w", err)
	}
	mutDir := filepath.Join(tmp, "mutant")
	mutScore, mutPose, mutPDBQT, err := dockTrack(run.ID, "mutant", ligPDBQT, pocket, mutDir)
	if err != nil {
		return nil, fmt.Errorf("dock: mutant track: %w", err)
	}

	dock := &models.LigandDock{
		SMILES:        smiles,
		WTScore:       round2(wtScore),
		MutantScore:   round2(mutScore),
		WTPosePDB:     wtPose,
		MutantPosePDB: mutPose,
	}

	// Covalent adjustment: when the mutated residue is a cysteine and the ligand
	// carries a warhead that docks within reach of the thiol, credit the covalent
	// bond on the mutant score. The WT track (no thiol) never earns this, so the
	// credit is the WT/mutant selectivity that non-covalent Vina cannot see.
	if isCovalentTarget(run.Mutagenesis.MutantResidue) {
		if adj, cov := applyCovalent(ctx, run, smiles, mutScore, mutPDBQT, mutDir); cov != nil {
			mutScore = adj
			dock.MutantScore = round2(adj)
			covDock := cov.CovalentDock
			dock.Covalent = &covDock
			if cov.posePDB != "" {
				dock.MutantPosePDB = cov.posePDB
			}
		}
	}

	dock.Selectivity = round2(wtScore - mutScore)
	return dock, nil
}

// applyCovalent runs the covalent assessment on the mutant docked pose and returns
// the (possibly credited) mutant score together with the CovalentDock record.
//
// It returns a record for EVERY warhead-bearing molecule, credited or not, so that
// "the warhead cannot reach the thiol" and "the measurement failed" stay visible
// instead of degrading into the same silent non-covalent result. Only a molecule
// with no warhead at all yields (mutScore, nil). The dock never errors on a covalent
// failure: a run that cannot model the bond still has a valid non-covalent score.
func applyCovalent(ctx context.Context, run *models.Run, smiles string, mutScore float64, mutPDBQT, outDir string) (float64, *covalentResult) {
	params := DefaultCovalentParams()
	target := resToken(run.Mutagenesis.MutantResidue, run.Mutagenesis.TargetResidueNum)
	record := func(status, warhead, note string) *covalentResult {
		return &covalentResult{CovalentDock: models.CovalentDock{
			TargetResidue:    target,
			WarheadType:      warhead,
			Status:           status,
			NonCovalentScore: round2(mutScore),
			Note:             note,
		}}
	}

	tetherOut := filepath.Join(outDir, "tether.pdb")
	a, err := assessCovalent(ctx, smiles, mutPDBQT,
		RunStructurePath(run.ID, "mutant"),
		run.Mutagenesis.TargetChain, run.Mutagenesis.TargetResidueNum,
		tetherOut, params.ReachMax)
	if err != nil {
		return mutScore, record(models.CovalentAssessFailed, "", truncate(err.Error(), 200))
	}
	if !a.HasWarhead {
		return mutScore, nil
	}
	switch a.Status {
	case assessNoThiol:
		return mutScore, record(models.CovalentNoThiol, a.WarheadType, "")
	case assessUnreadable:
		return mutScore, record(models.CovalentUnreadable, a.WarheadType,
			fmt.Sprintf("no warhead atom located across %d docked modes", a.ModesRead))
	case assessMeasured:
		// fall through
	default:
		return mutScore, record(models.CovalentAssessFailed, a.WarheadType,
			fmt.Sprintf("unexpected assessment status %q", a.Status))
	}
	if a.ReachDistance == nil {
		return mutScore, record(models.CovalentAssessFailed, a.WarheadType, "measured status carried no reach distance")
	}

	reach := *a.ReachDistance
	credit := covalentCredit(reach, params)
	if credit <= 0 {
		out := record(models.CovalentOutOfReach, a.WarheadType, "")
		out.ReachDistance = round2(reach)
		return mutScore, out
	}

	adjusted := mutScore - credit
	cov := &covalentResult{
		CovalentDock: models.CovalentDock{
			TargetResidue:    target,
			WarheadType:      a.WarheadType,
			Status:           models.CovalentInReach,
			ReachDistance:    round2(reach),
			Credit:           round2(credit),
			NonCovalentScore: round2(mutScore),
			Note:             truncate(a.TetherError, 200),
		},
	}
	// The tethered pose only supersedes the docked pose when the helper actually
	// closed the S–C bond without driving the ligand into the receptor.
	if a.TetherWritten {
		if b, e := os.ReadFile(tetherOut); e == nil {
			cov.posePDB = string(b)
			cov.Status = models.CovalentTethered
			cov.BondDistance = round2(a.BondDistance)
			cov.Note = ""
		}
	}
	return adjusted, cov
}

// truncate bounds a note so a runaway helper message cannot bloat the stored record.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// covalentResult carries the persisted CovalentDock plus the tethered pose PDB,
// which lives on LigandDock.MutantPosePDB rather than in CovalentDock.
type covalentResult struct {
	models.CovalentDock
	posePDB string
}

// Screening docking parameters — lower exhaustiveness than a one-off dock, since
// the generation loop scores many molecules and only needs reliable relative
// ranking, not final-quality poses.
const (
	screenExhaustiveness = 8
	screenCPU            = 2
)

// screenNumModes is how many binding modes both tracks report. The extra modes
// don't change the best (mode-1) score used for selectivity; they give the covalent
// reach scan lower-ranked poses to inspect for a warhead orientation that reaches
// the thiol.
const screenNumModes = 20

// screenVinaOptions are shared by both tracks: identical box, seed and mode count,
// so a WT/mutant score difference can only come from the receptor.
var screenVinaOptions = VinaOptions{
	Exhaustiveness: screenExhaustiveness,
	CPU:            screenCPU,
	Seed:           DefaultDockSeed,
	NumModes:       screenNumModes,
}

// dockTrack docks the prepared ligand into a run's structure for one track, reusing
// the run's cached receptor PDBQT (prepared once via ensureReceptorPDBQT), and
// returns the Vina affinity, the docked-pose PDB, and the multi-mode docked PDBQT
// path (for covalent reach assessment; valid until the caller cleans outDir).
func dockTrack(runID, track, ligandPDBQT string, pocket models.Pocket, outDir string) (float64, string, string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, "", "", err
	}
	receptorPDBQT, err := ensureReceptorPDBQT(runID, track)
	if err != nil {
		return 0, "", "", fmt.Errorf("receptor prep: %w", err)
	}
	res, err := RunVinaDock(receptorPDBQT, ligandPDBQT, pocket, screenVinaOptions, outDir)
	if err != nil {
		return 0, "", "", err
	}
	pose, _ := os.ReadFile(res.DockedPDB)
	return res.BindingAffinity, string(pose), res.DockedPDBQT, nil
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
