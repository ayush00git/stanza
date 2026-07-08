package services

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// skipUnlessValidator skips the test unless python3 and the RDKit validator are
// actually runnable. The real script is exercised (it is the contract that matters),
// but a host without RDKit — e.g. a bare CI runner — skips rather than fails.
// It also switches cwd to the repo root, since validateScript is resolved relative
// to the server's working directory and `go test` runs in the package directory.
func skipUnlessValidator(t *testing.T) {
	t.Helper()
	t.Chdir("..") // services/ -> repo root, where scripts/ lives
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	probe := exec.Command("python3", validateScript)
	probe.Stdin = strings.NewReader(`{"smiles":[]}`)
	if err := probe.Run(); err != nil {
		t.Skipf("RDKit validator not runnable (try `pip install -r scripts/requirements.txt`): %v", err)
	}
}

func TestValidateSMILESVerdicts(t *testing.T) {
	skipUnlessValidator(t)

	const aspirin = "CC(=O)Oc1ccccc1C(=O)O"
	batch := []string{
		aspirin,          // valid, drug-like        -> kept
		"not_a_molecule", // junk                     -> invalid_smiles
		"c1ccccc1",       // benzene, MW 78 (< 150)   -> mw_out_of_range
		aspirin,          // repeat of the first      -> duplicate
	}

	got, err := ValidateSMILES(context.Background(), "run_test", batch, nil)
	if err != nil {
		t.Fatalf("ValidateSMILES: %v", err)
	}
	if len(got) != len(batch) {
		t.Fatalf("got %d verdicts, want %d (one per input, in order)", len(got), len(batch))
	}

	// [0] aspirin — valid, kept, canonical echoed, drug-likeness populated.
	if v := got[0]; !v.Valid || !v.Kept || v.DropReason != "" {
		t.Errorf("aspirin: valid=%v kept=%v drop=%q, want valid+kept, no drop", v.Valid, v.Kept, v.DropReason)
	}
	if got[0].SMILES != aspirin {
		t.Errorf("aspirin canonical = %q, want %q", got[0].SMILES, aspirin)
	}
	if got[0].QED == nil || got[0].RO5Pass == nil || got[0].MolWeight == nil {
		t.Errorf("aspirin: expected qed/ro5/mw populated, got %+v", got[0])
	}

	// [1] junk — invalid, no properties, invalid_smiles.
	if v := got[1]; v.Valid || v.Kept || v.DropReason != "invalid_smiles" {
		t.Errorf("junk: valid=%v kept=%v drop=%q, want invalid_smiles", v.Valid, v.Kept, v.DropReason)
	}
	if got[1].QED != nil || got[1].InChIKey != "" {
		t.Errorf("junk: expected no qed/inchikey, got %+v", got[1])
	}

	// [2] benzene — valid but dropped for size.
	if v := got[2]; !v.Valid || v.Kept || v.DropReason != "mw_out_of_range" {
		t.Errorf("benzene: valid=%v kept=%v drop=%q, want valid, not kept, mw_out_of_range", v.Valid, v.Kept, v.DropReason)
	}

	// [3] duplicate of [0] — same identity, dropped as duplicate.
	if v := got[3]; !v.Valid || v.Kept || v.DropReason != "duplicate" {
		t.Errorf("dup: valid=%v kept=%v drop=%q, want valid, not kept, duplicate", v.Valid, v.Kept, v.DropReason)
	}
	if got[3].InChIKey != got[0].InChIKey || got[0].InChIKey == "" {
		t.Errorf("dup: inchikey %q != first %q (or empty)", got[3].InChIKey, got[0].InChIKey)
	}
}

func TestValidateSMILESCrossBatchDedup(t *testing.T) {
	skipUnlessValidator(t)

	const aspirin = "CC(=O)Oc1ccccc1C(=O)O"

	// First batch establishes the identity.
	first, err := ValidateSMILES(context.Background(), "run_test", []string{aspirin}, nil)
	if err != nil {
		t.Fatalf("ValidateSMILES (first): %v", err)
	}
	if len(first) != 1 || !first[0].Kept {
		t.Fatalf("first batch: expected aspirin kept, got %+v", first)
	}

	// Second batch, with the first InChIKey passed in as already-seen, must drop it.
	second, err := ValidateSMILES(context.Background(), "run_test", []string{aspirin}, []string{first[0].InChIKey})
	if err != nil {
		t.Fatalf("ValidateSMILES (second): %v", err)
	}
	if len(second) != 1 || second[0].Kept || second[0].DropReason != "duplicate" {
		t.Errorf("cross-batch: expected duplicate drop, got %+v", second[0])
	}
}

func TestValidateSMILESEmptyBatch(t *testing.T) {
	// No subprocess should run for an empty batch.
	got, err := ValidateSMILES(context.Background(), "run_test", nil, nil)
	if err != nil {
		t.Fatalf("ValidateSMILES(empty): %v", err)
	}
	if got != nil {
		t.Errorf("empty batch: got %v, want nil", got)
	}
}
