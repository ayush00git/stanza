package services

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ayush00git/stanza/models"
)

// DockLigandDualTrack is Stage 4. It docks one SMILES ligand into BOTH tracks of a
// run — the WT structure and the mutant structure — using the resistance pocket as
// the docking box, and returns the paired affinities, the selectivity margin
// (wt_score - mutant_score), and both poses. The box center is shared: the two
// structures differ by a single side chain, so selectivity comes from the receptor
// (the mutated residue), not from moving the box.
func DockLigandDualTrack(ctx context.Context, run *models.Run, smiles string) (*models.LigandDock, error) {
	return DockLigandDualTrackProgress(ctx, run, smiles, nil)
}

// ProgressFunc receives each completed docking step. It is called from the seed pool's
// goroutines, so implementations must be safe to call concurrently — reporter() below
// serialises them before they reach here.
type ProgressFunc func(models.DockProgress)

// reporter serialises progress callbacks and counts the steps, so a caller streaming to
// a browser never sees two writes interleave or a step index go backwards.
func reporter(onProgress ProgressFunc, total int) func(stage, message string, partial *models.DockPartial) {
	if onProgress == nil {
		return func(string, string, *models.DockPartial) {}
	}
	var mu sync.Mutex
	done := 0
	return func(stage, message string, partial *models.DockPartial) {
		mu.Lock()
		done++
		p := models.DockProgress{Stage: stage, Message: message, Done: done, Total: total, Partial: partial}
		mu.Unlock()
		onProgress(p)
	}
}

// DockLigandDualTrackProgress is DockLigandDualTrack with a step callback, so a slow
// dock can report what it is doing instead of hiding behind a spinner. onProgress may
// be nil.
func DockLigandDualTrackProgress(ctx context.Context, run *models.Run, smiles string, onProgress ProgressFunc) (*models.LigandDock, error) {
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

	// The covalent geometry assessment is the expensive Python pass, so it runs only for a
	// ligand that can actually bond the target — a SMILES substructure check settles that
	// in ~0.2s, before any docking. The docking replicates, however, are unconditional:
	// they guard the affinity against Vina's occasional bad local minimum, which afflicts
	// a non-covalent molecule exactly as much.
	covalent := isCovalentTarget(run.Mutagenesis.MutantResidue) && ligandHasWarhead(ctx, smiles)

	// ligand prep + WT dock + one step per mutant seed + the mutant score, plus the
	// covalent assessment. The scoring step is instant; it exists so the stream can hand
	// over the mutant affinity and the selectivity the moment they are real.
	total := 3 + len(screenSeeds)
	if covalent {
		total++
	}
	step := reporter(onProgress, total)

	// Prepare the ligand once; both docks reuse it.
	ligPDB, err := SMILESTo3D(smiles, tmp)
	if err != nil {
		return nil, fmt.Errorf("dock: ligand 3D generation: %w", err)
	}
	ligPDBQT, err := PrepareLigand(ligPDB, tmp)
	if err != nil {
		return nil, fmt.Errorf("dock: ligand prep: %w", err)
	}
	step("ligand", "3D conformer prepared", nil)

	// Both tracks share the ligand, the box and the seed list, so a WT/mutant affinity
	// difference can only come from the receptor — provided the search found each track's
	// deepest pose. It does not always: see bestReplicate. The per-track spread over seeds
	// is carried alongside the score so a margin can be read against the noise that
	// produced it, rather than in place of it.
	wt, err := dockTrack(run.ID, "wt", ligPDBQT, pocket, filepath.Join(tmp, "wt"), screenSeeds, nil)
	if err != nil {
		return nil, fmt.Errorf("dock: WT track: %w", err)
	}
	wtBest := bestReplicate(wt)
	wtScore := round2(wtBest.affinity)
	wtSpread := round2(spread(affinities(wt)))
	step("wt", fmt.Sprintf("wild-type affinity %.2f kcal/mol (best of %d seeds, spread %.2f)", wtScore, len(wt), wtSpread),
		&models.DockPartial{WTScore: &wtScore})

	mutDir := filepath.Join(tmp, "mutant")
	var seedsDone atomic.Int32
	mut, err := dockTrack(run.ID, "mutant", ligPDBQT, pocket, mutDir, screenSeeds, func() {
		n := seedsDone.Add(1)
		step("mutant", fmt.Sprintf("mutant pocket docked, seed %d of %d", n, len(screenSeeds)), nil)
	})
	if err != nil {
		return nil, fmt.Errorf("dock: mutant track: %w", err)
	}

	mutBest := bestReplicate(mut)
	mutScore := round2(mutBest.affinity)
	mutSpread := round2(spread(affinities(mut)))
	selectivity := round2(wtScore - mutScore)
	step("mutant", fmt.Sprintf("mutant affinity %.2f kcal/mol (best of %d seeds, spread %.2f)", mutScore, len(mut), mutSpread),
		&models.DockPartial{MutantScore: &mutScore, Selectivity: &selectivity})

	dock := &models.LigandDock{
		SMILES:        smiles,
		WTScore:       wtScore,
		MutantScore:   mutScore,
		WTSpread:      wtSpread,
		MutantSpread:  mutSpread,
		Replicates:    len(screenSeeds),
		WTPosePDB:     wtBest.posePDB,
		MutantPosePDB: mutBest.posePDB,
	}

	// A cancelled dock must fail, not publish. assessCovalentGeometry treats a failed
	// helper as a covalent verdict ("assess_failed"), which is the right answer when the
	// chemistry genuinely could not be assessed and the wrong one when the process was
	// simply killed. Left unguarded, a client closing its stream turned a perfectly good
	// haloacetamide into "not assessed" — a silent failure wearing the costume of a result.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dock: cancelled before the covalent assessment: %w", err)
	}

	// Covalent geometry: report whether the warhead can actually attack the thiol. This
	// is recorded BESIDE the affinity, never inside it — the covalent bond is not a Vina
	// energy, and a constant folded into MutantScore would turn Selectivity into a
	// restatement of that constant.
	if covalent {
		if cov := assessCovalentGeometry(ctx, run, smiles, mut, mutDir); cov != nil {
			covDock := cov.CovalentDock
			dock.Covalent = &covDock
			if cov.posePDB != "" {
				dock.MutantPosePDB = cov.posePDB
			}
			step("covalent", covalentProgressMessage(&covDock), &models.DockPartial{Covalent: &covDock})
		} else {
			step("covalent", "no warhead detected", nil)
		}
	}

	// Selectivity is the NON-COVALENT margin, and for a covalent target it is expected
	// to be ~0: Gly12→Cys12 barely perturbs the reversible contact set, and pan-KRAS
	// binders engage WT, G12C, G12D, G12V and G13D at indistinguishable affinity. A
	// covalent inhibitor's real selectivity is kinetic and lives in Covalent, not here.
	dock.Selectivity = selectivity
	return dock, nil
}

// covalentProgressMessage narrates the covalent verdict for the progress stream. The
// user has just waited most of a minute; the least the last step can do is say what the
// warhead did rather than "done".
func covalentProgressMessage(c *models.CovalentDock) string {
	switch c.Status {
	case models.CovalentTethered, models.CovalentFeasible:
		if c.Uncertain {
			return fmt.Sprintf("%s warhead: attack geometry flips with the docking seed", c.WarheadType)
		}
		return fmt.Sprintf("%s warhead can attack %s (feasibility %.2f, reach %.2f Å)",
			c.WarheadType, c.TargetResidue, c.Feasibility, c.ReachDistance)
	case models.CovalentInfeasible:
		return fmt.Sprintf("%s warhead cannot attack %s (reach %.2f Å, angle %.0f°)",
			c.WarheadType, c.TargetResidue, c.ReachDistance, c.AttackAngle)
	default:
		return fmt.Sprintf("covalent geometry %s", c.Status)
	}
}

// ligandHasWarhead reports whether a SMILES carries a cysteine-reactive warhead.
//
// A detection failure is treated as "yes". The false positive costs two extra docks and
// then reports assess_failed honestly; the false negative would silently skip the
// covalent assessment altogether and hand back a molecule labelled non-covalent — the
// exact class of silent negative this pipeline has already been bitten by twice.
func ligandHasWarhead(ctx context.Context, smiles string) bool {
	has, _, err := HasCovalentWarhead(ctx, smiles)
	return err != nil || has
}

// replicate is one seed's docking of a ligand into one track.
type replicate struct {
	seed      int
	affinity  float64
	posePDB   string
	posePDBQT string
}

// bestReplicate returns the replicate with the LOWEST affinity — the deepest pose any
// seed found.
//
// This was a median until a molecule read selectivity +2.39 against a mutation that
// cannot change reversible binding. Seven seeds explained it: BOTH tracks were bimodal,
// and the mutant track found its deep basin (≈ −9.35) in 5 of 7 seeds while the wild-type
// track found its own deep basin (−9.23) in 1 of 7. The pockets bound the ligand almost
// identically; Vina's SEARCH found one pose more often than the other. A median reports
// the basin the search lands in most often. Their true minima differ by 0.19 kcal/mol —
// which is the ≈0 the covalent theory demands.
//
// Vina is a minimiser. Its affinity estimates a global minimum, so a low outlier is not
// noise to be resisted — it is the best available estimate of the answer, and discarding
// it manufactures selectivity out of sampling asymmetry between the two tracks.
//
// Best-of-N is downward-biased (more seeds, more chances to find a deeper pose). That
// bias cancels in wt − mut only when both tracks are equally searchable, which is exactly
// what fails here — so the spread over seeds travels with the score, and a caller that
// wants to know whether to trust a margin must look at it.
func bestReplicate(reps []replicate) replicate {
	return slices.MinFunc(reps, func(a, b replicate) int { return cmp.Compare(a.affinity, b.affinity) })
}

// affinities extracts the per-seed affinities of a track, for spread reporting.
func affinities(reps []replicate) []float64 {
	out := make([]float64, len(reps))
	for i, r := range reps {
		out[i] = r.affinity
	}
	return out
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

	// Build the tether from the best-affinity replicate, the same pose the viewer shows.
	// It only supersedes the docked pose when the helper actually closed the S–C bond
	// without driving the ligand into the receptor.
	tetherOut := filepath.Join(outDir, "tether.pdb")
	if a, err := assessCovalent(ctx, smiles, bestReplicate(reps).posePDBQT,
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

// Screening docking parameters.
//
// screenExhaustiveness is 16 rather than Vina's default 8. At 8 the search is bimodal on
// some ligands: it finds either a deep pose with the warhead 5.8 Å from the thiol, or a
// shallower one at 3.85 Å, depending on the seed — and the covalent verdict follows.
// Measured over five seeds on two molecules from a real run, at the box the pipeline
// actually passes Vina:
//
//	                     exhaustiveness 8              exhaustiveness 16
//	unsaturated amide    reach spread 0.81 Å,          reach spread 0.10 Å,
//	                     one seed feasible → straddles  every seed infeasible → stable
//	acrylamide           2 of 5 seeds infeasible;      1 of 5 seeds infeasible;
//	                     3 of 10 three-seed subsets    0 of 10 subsets flip it
//	                     flip the verdict
//
// Doubling exhaustiveness costs roughly twice the CPU per dock, which the concurrent seed
// pool absorbs. Replicating seeds cannot fix a bimodal search — the seeds resample the
// same two basins — so this is the lever, and `uncertain` remains the backstop for the
// ligands that stay genuinely ambiguous.
//
// screenCPU is pinned rather than scaled to free cores: Vina is bit-deterministic given
// (seed, cpu count), and a reproducible geometry is the entire point of replicating.
const (
	screenExhaustiveness = 16
	screenCPU            = 2
)

// screenNumModes is how many binding modes both tracks report. The extra modes
// don't change the best (mode-1) score used for selectivity; they give the covalent
// reach scan lower-ranked poses to inspect for a warhead orientation that reaches
// the thiol.
const screenNumModes = 20

// screenSeeds are the replicate seeds BOTH tracks are docked under.
//
// Two separate quantities need them.
//
// The covalent GEOMETRY is noisy: with the ligand conformer held fixed, the warhead's
// closest approach to the thiol varied by ±0.16–1.09 Å over five seeds, and on one
// molecule the covalent call flipped outright on nothing but the RNG. Feasibility is a
// function of that geometry, so a single-seed answer is a coin toss reported to two
// decimals.
//
// The AFFINITY is noisy too, which cost us a wrong answer. Vina's affinity is *usually*
// reproducible — sd ~0.03 kcal/mol on the molecules first measured — so the wild-type
// track was once docked under a single seed on the argument that extra seeds re-measure
// the same number. They do not. Vina's search occasionally settles in a bad local
// minimum, and it does so per (molecule, receptor, seed): on
// C=C(F)C(=O)N1CCN(c2nc(-c3cccc4c(O)cccc34)nc3c2ncn3C)CC1 seed 42 scored the wild-type
// pocket at −8.75 where four other seeds agreed on −9.8, while the mutant track's seed
// 1337 scored −7.84 against a −9.86 consensus. The mutant survived because three seeds
// let the median discard its outlier. The wild type, docked once, reported the outlier
// as fact — and the run showed a +1.03 kcal/mol selectivity for a molecule whose real
// margin is +0.09.
//
// So every track is replicated, and every reported affinity is a median. Three seeds
// rather than five: an odd count keeps the median unambiguous, one outlier is still
// outvoted, and the pool is three wide so a track costs one batch either way.
var screenSeeds = []int{42, 1337, 7}

// maxParallelDocks bounds the seed replicates run at once. Each Vina process is pinned
// to screenCPU cores, so this must stay small enough that the pool does not oversubscribe
// the machine — an oversubscribed Vina is slower, not faster.
const maxParallelDocks = 3

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

// dockTrack docks the prepared ligand into a run's structure for one track, once per
// replicate seed, reusing the run's cached receptor PDBQT (prepared once via
// ensureReceptorPDBQT). The returned pose paths stay valid until the caller cleans
// outDir.
//
// The seeds run concurrently. Each writes into its own seed directory, and the receptor
// PDBQT is prepared before the pool starts so the replicates only ever read it — Vina
// itself is deterministic given (seed, cpu count), which is why screenCPU is pinned
// rather than scaled to whatever cores happen to be free.
// onSeed, when non-nil, fires as each replicate finishes. It is called from the pool's
// goroutines and must be safe to call concurrently.
func dockTrack(runID, track, ligandPDBQT string, pocket models.Pocket, outDir string, seeds []int, onSeed func()) ([]replicate, error) {
	receptorPDBQT, err := ensureReceptorPDBQT(runID, track)
	if err != nil {
		return nil, fmt.Errorf("receptor prep: %w", err)
	}

	// Indexed writes, so the replicate order is the seed order regardless of which
	// goroutine finishes first — a median must not depend on scheduling.
	reps := make([]replicate, len(seeds))
	errs := make([]error, len(seeds))

	var wg sync.WaitGroup
	slot := make(chan struct{}, maxParallelDocks)
	for i, seed := range seeds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slot <- struct{}{}
			defer func() { <-slot }()

			seedDir := filepath.Join(outDir, "seed"+strconv.Itoa(seed))
			if err := os.MkdirAll(seedDir, 0o755); err != nil {
				errs[i] = err
				return
			}
			res, err := RunVinaDock(receptorPDBQT, ligandPDBQT, pocket, screenVinaOptions(seed), seedDir)
			if err != nil {
				errs[i] = fmt.Errorf("seed %d: %w", seed, err)
				return
			}
			pose, _ := os.ReadFile(res.DockedPDB)
			reps[i] = replicate{
				seed:      seed,
				affinity:  res.BindingAffinity,
				posePDB:   string(pose),
				posePDBQT: res.DockedPDBQT,
			}
			if onSeed != nil {
				onSeed()
			}
		}()
	}
	wg.Wait()

	// One failed seed loses that replicate, not the dock: the median and the spread are
	// still meaningful over the survivors. Only a total failure is an error.
	live := reps[:0]
	for i := range reps {
		if errs[i] == nil {
			live = append(live, reps[i])
		}
	}
	if len(live) == 0 {
		return nil, fmt.Errorf("every replicate failed: %w", errors.Join(errs...))
	}
	return live, nil
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
