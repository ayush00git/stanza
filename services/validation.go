package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// validateScript is the RDKit pre-filter, resolved relative to the server's working
// directory (the repo root, like scripts/mutate.py and the fpocket ./tmp scratch dir).
const validateScript = "scripts/validate.py"

// MoleculeVerdict is one molecule's RDKit validation result. It mirrors, field for
// field, the per-molecule object emitted by scripts/validate.py. The pointer fields
// are nil for an invalid molecule (which has no computed properties); SAScore is
// additionally nil when the optional SA scorer is unavailable.
type MoleculeVerdict struct {
	SMILES     string   `json:"smiles"`   // canonical form when valid; raw input when not
	InChIKey   string   `json:"inchikey"` // "" when invalid
	Valid      bool     `json:"valid"`    // parsed + sanitized
	Kept       bool     `json:"kept"`     // survived every filter → eligible for docking
	QED        *float64 `json:"qed"`
	RO5Pass    *bool    `json:"ro5_pass"`
	SAScore    *float64 `json:"sa_score"`
	MolWeight  *float64 `json:"mol_weight"`
	LogP       *float64 `json:"logp"`
	DropReason string   `json:"drop_reason"` // "" when kept
}

// ValidateSMILES is Stage 5. It runs the RDKit pre-filter over a batch of raw SMILES
// for a run and returns one verdict per input, in input order: invalid, duplicate,
// and non-drug-like molecules are flagged so callers can drop them before spending
// the (expensive) dock budget. seenInChIKeys carries identities already known for the
// run so dedupe spans calls, not just this batch. Go has no cheminformatics library,
// so it shells out to scripts/validate.py, mirroring the mutate.py pattern.
func ValidateSMILES(ctx context.Context, runID string, smiles, seenInChIKeys []string) ([]MoleculeVerdict, error) {
	if len(smiles) == 0 {
		return nil, nil
	}

	in, err := json.Marshal(map[string]any{
		"run_id":         runID,
		"smiles":         smiles,
		"seen_inchikeys": seenInChIKeys,
	})
	if err != nil {
		return nil, fmt.Errorf("validate: marshal input: %w", err)
	}

	cmd := exec.CommandContext(ctx, "python3", validateScript)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("validate: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var out struct {
		Molecules []MoleculeVerdict `json:"molecules"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("validate: parse output: %w", err)
	}
	return out.Molecules, nil
}
