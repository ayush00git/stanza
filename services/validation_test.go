package services

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/ayush00git/stanza/models"
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

	got, err := ValidateSMILES(context.Background(), "run_test", batch, nil, nil)
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
	first, err := ValidateSMILES(context.Background(), "run_test", []string{aspirin}, nil, nil)
	if err != nil {
		t.Fatalf("ValidateSMILES (first): %v", err)
	}
	if len(first) != 1 || !first[0].Kept {
		t.Fatalf("first batch: expected aspirin kept, got %+v", first)
	}

	// Second batch, with the first InChIKey passed in as already-seen, must drop it.
	second, err := ValidateSMILES(context.Background(), "run_test", []string{aspirin}, []string{first[0].InChIKey}, nil)
	if err != nil {
		t.Fatalf("ValidateSMILES (second): %v", err)
	}
	if len(second) != 1 || second[0].Kept || second[0].DropReason != "duplicate" {
		t.Errorf("cross-batch: expected duplicate drop, got %+v", second[0])
	}
}

func TestValidateSMILESEmptyBatch(t *testing.T) {
	// No subprocess should run for an empty batch.
	got, err := ValidateSMILES(context.Background(), "run_test", nil, nil, nil)
	if err != nil {
		t.Fatalf("ValidateSMILES(empty): %v", err)
	}
	if got != nil {
		t.Errorf("empty batch: got %v, want nil", got)
	}
}

// A pre-filter that discards the approved drug cannot be used to judge a molecule
// designed to resemble it. Every clinical KRAS switch-II inhibitor breaks the default
// rule-of-five gate: sotorasib is 533 Da, ARS-1620 540 Da, adagrasib 574 Da with two
// rule-of-five violations and a QED of 0.27. The curated site's weight window has to
// reach the pre-filter, or the generation prompt asks for 430–620 Da while everything
// above 500 Da is silently dropped.
func TestCuratedThresholdsAdmitTheRealG12CInhibitors(t *testing.T) {
	skipUnlessValidator(t)

	drugs := map[string]string{
		"sotorasib": "CC1=CC=CC(F)=C1C1=C(C(=O)N2C(C)COCC2C)C2=NC(N3CCN(C(=O)C=C)C[C@@H]3C)=NC=C2N=C1",
		"adagrasib": "CN1CCC[C@@H]1COC1=NC(N2CCN(C(=O)C=C)C[C@@H]2C)=C2C=CC(Cl)=C(C3=CC=CC4=C3C=CC=C4F)C2=N1",
		"ARS-1620":  "CC1CN(C(=O)C=C)CCN1C1=NC(=NC2=C1C=CC(=C2Cl)C1=C(F)C=CC=C1O)OC[C@@H]1CCCN1C",
	}
	names := make([]string, 0, len(drugs))
	batch := make([]string, 0, len(drugs))
	for n, s := range drugs {
		names = append(names, n)
		batch = append(batch, s)
	}

	// Default thresholds: every one of them is discarded.
	base, err := ValidateSMILES(context.Background(), "run_defaults", batch, nil, nil)
	if err != nil {
		t.Fatalf("ValidateSMILES(defaults): %v", err)
	}
	for i, v := range base {
		if v.Kept {
			t.Errorf("%s survived the default pre-filter; the 500 Da ceiling was expected to drop it", names[i])
		}
	}

	// The curated KRAS G12C window admits all three.
	site := LookupKnownSite("P01116", models.Mutation{WildType: "G", Position: 12, Mutant: "C"}, "")
	th := siteThresholds(site)
	if th == nil {
		t.Fatal("the curated switch-II site declares no weight window")
	}
	got, err := ValidateSMILES(context.Background(), "run_curated", batch, nil, th)
	if err != nil {
		t.Fatalf("ValidateSMILES(curated): %v", err)
	}
	for i, v := range got {
		if !v.Kept {
			t.Errorf("%s dropped by the curated pre-filter (reason %q); it is an approved or clinical G12C inhibitor",
				names[i], v.DropReason)
		}
	}
}

// A site with no declared weight window must not silently widen the gate.
func TestSiteThresholdsAreNilWithoutGuidance(t *testing.T) {
	if siteThresholds(nil) != nil {
		t.Error("a nil site produced thresholds")
	}
	if siteThresholds(&KnownSite{Name: "bare"}) != nil {
		t.Error("a site with no guidance produced thresholds")
	}
}
