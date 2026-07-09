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

// CovalentParams tune the covalent credit. The credit decays linearly with the
// warhead-to-thiol reach distance: full credit when the warhead is positioned to
// bond, none once it is too far to engage, so a better-positioned warhead scores
// better and the generation loop has a gradient to optimise toward.
type CovalentParams struct {
	ReachIdeal float64 // ≤ this (Å): warhead positioned to bond → full credit
	ReachMax   float64 // > this (Å): warhead cannot engage the thiol → no credit
	MaxCredit  float64 // kcal/mol credit at ideal geometry
}

// DefaultCovalentParams returns the calibrated covalent-credit defaults. The reach
// window (3.5–5.0 Å) brackets the near-attack S···C distance of a docked warhead
// pose; the 4.0 kcal/mol ceiling gives covalent binders a clear, but not absurd,
// selectivity margin over non-covalent molecules docked into the same pocket.
func DefaultCovalentParams() CovalentParams {
	return CovalentParams{ReachIdeal: 3.5, ReachMax: 5.0, MaxCredit: 4.0}
}

// covalentCredit maps a warhead-reactive-carbon → thiol-SG reach distance (Å) to a
// covalent credit (kcal/mol): MaxCredit at/below ReachIdeal, linearly to 0 at
// ReachMax, and 0 beyond. A non-positive reach window yields no credit.
func covalentCredit(reach float64, p CovalentParams) float64 {
	if p.MaxCredit <= 0 || p.ReachMax <= p.ReachIdeal {
		return 0
	}
	if reach <= p.ReachIdeal {
		return p.MaxCredit
	}
	if reach >= p.ReachMax {
		return 0
	}
	return p.MaxCredit * (p.ReachMax - reach) / (p.ReachMax - p.ReachIdeal)
}

// isCovalentTarget reports whether a mutated residue is a nucleophile our warheads
// react with. The warhead SMARTS in covalent.py are cysteine-thiol Michael
// acceptors and SN2 electrophiles, so only cysteine qualifies today; adding Ser/Lys
// warheads later widens this set.
func isCovalentTarget(residue3 string) bool {
	return strings.EqualFold(strings.TrimSpace(residue3), "CYS")
}

// covalentAssessment mirrors the JSON emitted by scripts/covalent.py `assess`.
type covalentAssessment struct {
	HasWarhead    bool     `json:"has_warhead"`
	WarheadType   string   `json:"warhead_type"`
	ReachDistance *float64 `json:"reach_distance"` // nil when no mode could be matched
	BondDistance  float64  `json:"bond_distance"`  // S–C of the emitted tether pose
	TetherWritten bool     `json:"tether_written"`
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

// assessCovalent runs scripts/covalent.py `assess`: it scans the mutant docked pose
// for the mode whose warhead reactive carbon comes closest to the cysteine SG, and
// (when tetherOut is set) writes the tethered covalent-complex pose there.
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
