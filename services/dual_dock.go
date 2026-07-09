package services

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

	// Both tracks are docked under the same replicate seeds, so a WT/mutant
	// difference can still only come from the receptor.
	wt, err := dockTrack(run.ID, "wt", ligPDBQT, pocket, filepath.Join(tmp, "wt"))
	if err != nil {
		return nil, fmt.Errorf("dock: WT track: %w", err)
	}
	mutDir := filepath.Join(tmp, "mutant")
	mut, err := dockTrack(run.ID, "mutant", ligPDBQT, pocket, mutDir)
	if err != nil {
		return nil, fmt.Errorf("dock: mutant track: %w", err)
	}

	wtBest, mutBest := medianReplicate(wt), medianReplicate(mut)
	wtScore, mutScore := wtBest.affinity, mutBest.affinity

	dock := &models.LigandDock{
		SMILES:        smiles,
		WTScore:       round2(wtScore),
		MutantScore:   round2(mutScore),
		WTPosePDB:     wtBest.posePDB,
		MutantPosePDB: mutBest.posePDB,
	}

	// Covalent geometry: when the mutated residue is a cysteine and the ligand carries
	// a warhead, report whether that warhead can actually attack the thiol. This is
	// recorded BESIDE the affinity, never inside it — the covalent bond is not a Vina
	// energy, and a constant folded into MutantScore would turn Selectivity into a
	// restatement of that constant.
	if isCovalentTarget(run.Mutagenesis.MutantResidue) {
		if cov := assessCovalentGeometry(ctx, run, smiles, mut, mutDir); cov != nil {
			covDock := cov.CovalentDock
			dock.Covalent = &covDock
			if cov.posePDB != "" {
				dock.MutantPosePDB = cov.posePDB
			}
		}
	}

	// Selectivity is the NON-COVALENT margin, and for a covalent target it is expected
	// to be ~0: Gly12→Cys12 barely perturbs the reversible contact set, and pan-KRAS
	// binders engage WT, G12C, G12D, G12V and G13D at indistinguishable affinity. A
	// covalent inhibitor's real selectivity is kinetic and lives in Covalent, not here.
	dock.Selectivity = round2(wtScore - mutScore)
	return dock, nil
}

// replicate is one seed's docking of a ligand into one track.
type replicate struct {
	seed      int
	affinity  float64
	posePDB   string
	posePDBQT string
}

// medianReplicate returns the replicate with the median affinity — a summary that a
// single outlying seed cannot drag around, unlike the mean or the best score.
func medianReplicate(reps []replicate) replicate {
	sorted := slices.Clone(reps)
	slices.SortFunc(sorted, func(a, b replicate) int { return cmp.Compare(a.affinity, b.affinity) })
	return sorted[len(sorted)/2]
}

// median of a float sample, and the spread (max − min) of that sample.
func median(xs []float64) float64 {
	sorted := slices.Clone(xs)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

func spread(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	return slices.Max(xs) - slices.Min(xs)
}

// assessCovalentGeometry runs the covalent assessment on every mutant replicate and
// returns the CovalentDock record describing whether the warhead can attack the thiol.
//
// It NEVER touches the mutant affinity. Vina has no covalent term, and a constant
// bolted onto the score would make the selectivity margin a restatement of that
// constant — which is precisely what the previous credit model did. Feasibility is
// reported alongside the score, not folded into it.
//
// A record comes back for EVERY warhead-bearing molecule, feasible or not, so that
// "the warhead cannot attack the thiol" and "the measurement failed" stay visible
// instead of degrading into the same silent non-covalent result. Only a molecule with
// no warhead at all yields nil. The dock never errors on a covalent failure: a run
// that cannot model the bond still has a valid non-covalent score.
func assessCovalentGeometry(ctx context.Context, run *models.Run, smiles string, reps []replicate, outDir string) *covalentResult {
	target := resToken(run.Mutagenesis.MutantResidue, run.Mutagenesis.TargetResidueNum)
	record := func(status, warhead, note string) *covalentResult {
		return &covalentResult{CovalentDock: models.CovalentDock{
			TargetResidue: target,
			WarheadType:   warhead,
			Status:        status,
			Replicates:    len(reps),
			Note:          note,
		}}
	}

	// Assess every replicate: the geometry is the noisy quantity, so one seed's answer
	// is not an answer. The tether is built only for the median replicate, below.
	var (
		measured []*covalentAssessment
		warhead  string
		firstErr error
		lastFail string
	)
	for _, rep := range reps {
		a, err := assessCovalent(ctx, smiles, rep.posePDBQT,
			RunStructurePath(run.ID, "mutant"),
			run.Mutagenesis.TargetChain, run.Mutagenesis.TargetResidueNum,
			"") // no tether on the scan pass
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !a.HasWarhead {
			return nil
		}
		warhead = a.WarheadType
		switch a.Status {
		case assessNoThiol:
			return record(models.CovalentNoThiol, warhead, "")
		case assessMeasured:
			if a.Feasibility != nil && a.ReachDistance != nil {
				measured = append(measured, a)
			}
		case assessUnreadable:
			lastFail = fmt.Sprintf("no warhead atom located across %d docked modes", a.ModesRead)
		default:
			lastFail = fmt.Sprintf("unexpected assessment status %q", a.Status)
		}
	}
	if len(measured) == 0 {
		if firstErr != nil {
			return record(models.CovalentAssessFailed, warhead, truncate(firstErr.Error(), 200))
		}
		return record(models.CovalentUnreadable, warhead, lastFail)
	}

	feasibilities := make([]float64, len(measured))
	reaches := make([]float64, len(measured))
	for i, a := range measured {
		feasibilities[i] = *a.Feasibility
		reaches[i] = *a.ReachDistance
	}

	// When some seeds find an attackable geometry and others do not, the covalent call
	// is decided by the RNG — surface that rather than letting the median stand in for
	// a fact.
	feasibility := median(feasibilities)
	uncertain := slices.Min(feasibilities) <= 0 && slices.Max(feasibilities) > 0

	// The replicate whose feasibility IS the median supplies the reported angle and
	// mode, so every number in the record comes from one real pose rather than being
	// averaged across poses that never coexisted.
	repr := slices.MinFunc(measured, func(a, b *covalentAssessment) int {
		return cmp.Compare(math.Abs(*a.Feasibility-feasibility), math.Abs(*b.Feasibility-feasibility))
	})

	cov := &covalentResult{CovalentDock: models.CovalentDock{
		TargetResidue: target,
		WarheadType:   warhead,
		Status:        models.CovalentFeasible,
		Feasibility:   round2(feasibility),
		ReachDistance: round2(median(reaches)),
		ReachSpread:   round2(spread(reaches)),
		AttackAngle:   round2(repr.AttackAngle),
		ModeRank:      repr.ModeRank,
		ModeAffinity:  round2(repr.ModeAffinity),
		Replicates:    len(measured),
		Uncertain:     uncertain,
	}}
	if feasibility <= 0 {
		cov.Status = models.CovalentInfeasible
		return cov
	}

	// Build the tether from the median-affinity replicate, the same pose the viewer
	// shows. It only supersedes the docked pose when the helper actually closed the
	// S–C bond without driving the ligand into the receptor.
	tetherOut := filepath.Join(outDir, "tether.pdb")
	if a, err := assessCovalent(ctx, smiles, medianReplicate(reps).posePDBQT,
		RunStructurePath(run.ID, "mutant"),
		run.Mutagenesis.TargetChain, run.Mutagenesis.TargetResidueNum,
		tetherOut); err == nil {
		if a.TetherWritten {
			if b, e := os.ReadFile(tetherOut); e == nil {
				cov.posePDB = string(b)
				cov.Status = models.CovalentTethered
				cov.BondDistance = round2(a.BondDistance)
			}
		} else {
			cov.Note = truncate(a.TetherError, 200)
		}
	}
	return cov
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

// screenSeeds are the replicate seeds each track is docked under.
//
// Vina's affinity is reproducible across seeds (sd ~0.03 kcal/mol) but the covalent
// REACH is not: with the ligand conformer held fixed, the warhead's closest approach
// to the thiol varied by ±0.16–1.09 Å over five seeds, and on one molecule the credit
// swung between 0.00 and 3.42 kcal/mol depending on nothing but the RNG. Since the
// whole selectivity margin of a covalent binder is a function of reach, a single-seed
// reach is a coin toss reported to two decimals. Replicating lets us take the median
// and, more importantly, report the spread.
var screenSeeds = []int{42, 1337, 7, 101, 2024}

// screenVinaOptions is the per-seed template; both tracks share box, mode count and
// the seed list, so a WT/mutant difference can still only come from the receptor.
func screenVinaOptions(seed int) VinaOptions {
	return VinaOptions{
		Exhaustiveness: screenExhaustiveness,
		CPU:            screenCPU,
		Seed:           seed,
		NumModes:       screenNumModes,
	}
}

// dockTrack docks the prepared ligand into a run's structure for one track once per
// replicate seed, reusing the run's cached receptor PDBQT (prepared once via
// ensureReceptorPDBQT). The returned pose paths stay valid until the caller cleans
// outDir.
func dockTrack(runID, track, ligandPDBQT string, pocket models.Pocket, outDir string) ([]replicate, error) {
	receptorPDBQT, err := ensureReceptorPDBQT(runID, track)
	if err != nil {
		return nil, fmt.Errorf("receptor prep: %w", err)
	}
	reps := make([]replicate, 0, len(screenSeeds))
	for _, seed := range screenSeeds {
		seedDir := filepath.Join(outDir, "seed"+strconv.Itoa(seed))
		if err := os.MkdirAll(seedDir, 0o755); err != nil {
			return nil, err
		}
		res, err := RunVinaDock(receptorPDBQT, ligandPDBQT, pocket, screenVinaOptions(seed), seedDir)
		if err != nil {
			return nil, err
		}
		pose, _ := os.ReadFile(res.DockedPDB)
		reps = append(reps, replicate{
			seed:      seed,
			affinity:  res.BindingAffinity,
			posePDB:   string(pose),
			posePDBQT: res.DockedPDBQT,
		})
	}
	if len(reps) == 0 {
		return nil, fmt.Errorf("no replicate produced a pose")
	}
	return reps, nil
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
