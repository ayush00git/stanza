package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Covalent docking for the mutant track. AutoDock Vina scores non-covalently and
// cannot see the covalent bond a warhead forms to a cysteine thiol — the entire
// selectivity mechanism of covalent inhibitors like sotorasib (which bond KRAS
// Cys12; wild-type Gly12 has no thiol, so the drug cannot attach). We recover that
// signal with an explicit, geometry-gated covalent credit: a molecule that carries
// a cysteine-reactive warhead AND docks with that warhead's reactive carbon within
// reach of the mutated cysteine's SG earns a bond credit on the mutant track. The
// wild-type track has no thiol and can never earn it, so the credit is exactly the
// WT/mutant asymmetry that non-covalent docking collapses to noise.
//
// The credit magnitude is a model parameter, not a Vina-computed energy: Vina has
// no covalent term to calibrate against. What IS physically computed is the
// geometry — whether the warhead can actually reach the thiol — via scripts/covalent.py.

// covalentScript is the RDKit helper, resolved relative to the server's working
// directory (the repo root, like scripts/mutate.py and scripts/validate.py).
const covalentScript = "scripts/covalent.py"

// isCovalentTarget reports whether a mutated residue is a nucleophile our warheads
// react with. The warhead SMARTS in covalent.py are cysteine-thiol Michael
// acceptors and SN2 electrophiles, so only cysteine qualifies today; adding Ser/Lys
// warheads later widens this set.
func isCovalentTarget(residue3 string) bool {
	return strings.EqualFold(strings.TrimSpace(residue3), "CYS")
}

// Statuses emitted by scripts/covalent.py `assess`. They exist so that "the ligand
// has no warhead", "the warhead is too far to bond" and "the pose could not be read"
// stay distinguishable: collapsing them into one silent nil is how a broken
// measurement passes itself off as a negative result.
// (a ligand with no warhead is reported via HasWarhead rather than a status)
const (
	assessNoThiol    = "no_thiol"
	assessUnreadable = "unreadable_pose"
	assessMeasured   = "measured"
)

// covalentAssessment mirrors the JSON emitted by scripts/covalent.py `assess`.
//
// All chemistry and geometry live in the script; Go never applies a threshold. The
// script returns Feasibility in [0,1] — 0 meaning the warhead cannot attack the
// thiol from any pose the receptor actually binds — together with the geometry that
// produced it, so the number can always be audited back to a distance, an angle and
// a named docking mode.
type covalentAssessment struct {
	HasWarhead    bool     `json:"has_warhead"`
	WarheadType   string   `json:"warhead_type"`
	Status        string   `json:"status"`
	Feasibility   *float64 `json:"feasibility"`    // nil unless Status == assessMeasured
	ReachDistance *float64 `json:"reach_distance"` // nil unless Status == assessMeasured
	AttackAngle   float64  `json:"attack_angle"`   // degrees at the electrophilic carbon
	ModeRank      int      `json:"mode_rank"`      // 1-based Vina mode the geometry came from
	ModeAffinity  float64  `json:"mode_affinity"`  // that mode's affinity (kcal/mol)
	ModesRead     int      `json:"modes_read"`
	BondDistance  float64  `json:"bond_distance"` // S–C of the emitted tether pose
	TetherRMSD    float64  `json:"tether_rmsd"`   // heavy-atom drift from the docked pose
	MinContact    float64  `json:"min_contact"`   // closest tethered-ligand → receptor contact
	TetherWritten bool     `json:"tether_written"`
	TetherError   string   `json:"tether_error"`
	Error         string   `json:"error"`
}

// HasCovalentWarhead reports whether a SMILES carries a cysteine-reactive warhead,
// and its class. Used to flag covalent candidates independently of docking.
func HasCovalentWarhead(ctx context.Context, smiles string) (bool, string, error) {
	cmd := exec.CommandContext(ctx, "python3", covalentScript, "detect", "--smiles", smiles)
	out, err := cmd.Output()
	if err != nil {
		return false, "", fmt.Errorf("covalent detect: %w", err)
	}
	var r covalentAssessment
	if err := json.Unmarshal(out, &r); err != nil {
		return false, "", fmt.Errorf("covalent detect: parse %q: %w", string(out), err)
	}
	if r.Error != "" {
		return false, "", fmt.Errorf("covalent detect: %s", r.Error)
	}
	return r.HasWarhead, r.WarheadType, nil
}

// assessCovalent runs scripts/covalent.py `assess`: across the docked modes that the
// receptor actually binds, it finds the pose from which the warhead's electrophilic
// carbon can attack the cysteine thiol, and scores that geometry as a feasibility in
// [0,1]. When tetherOut is set it also writes the tethered covalent-complex pose (the
// script skips the tether for an infeasible warhead, since forcing a bond onto such a
// pose only yields a distorted structure).
func assessCovalent(ctx context.Context, smiles, posePDBQT, receptorPDB, chain string, resnum int, tetherOut string) (*covalentAssessment, error) {
	args := []string{covalentScript, "assess",
		"--smiles", smiles,
		"--pose", posePDBQT,
		"--receptor", receptorPDB,
		"--chain", chain,
		"--resnum", strconv.Itoa(resnum),
	}
	if tetherOut != "" {
		args = append(args, "--tether-out", tetherOut)
	}
	cmd := exec.CommandContext(ctx, "python3", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("covalent assess: %w", err)
	}
	var r covalentAssessment
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("covalent assess: parse %q: %w", string(out), err)
	}
	if r.Error != "" {
		return nil, fmt.Errorf("covalent assess: %s", r.Error)
	}
	return &r, nil
}
