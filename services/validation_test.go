package services

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

// priorArt is the curated reference set, read from the same PubChem-sourced file
// scripts/novelty.py uses. The structures are NOT written out here on purpose: an
// earlier revision of this test hand-typed them, and all three were plausible-looking
// molecules that were not the drugs they claimed to be — wrong InChIKey skeleton,
// wrong mass by 30–110 Da. A test that guards the pre-filter with invented compounds
// guards nothing. Re-fetch by CID; never retype.
type priorArtCompound struct {
	Name   string  `json:"name"`
	CID    int     `json:"cid"`
	MW     float64 `json:"mw"`
	SMILES string  `json:"smiles"`
}

func priorArt(t *testing.T) []priorArtCompound {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("data", "prior_art_kras_g12c.json"))
	if err != nil {
		t.Fatalf("read prior art: %v", err)
	}
	var doc struct {
		Compounds []priorArtCompound `json:"compounds"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse prior art: %v", err)
	}
	if len(doc.Compounds) == 0 {
		t.Fatal("prior art file declares no compounds")
	}
	return doc.Compounds
}

// A pre-filter that discards the approved drug cannot be used to judge a molecule
// designed to resemble it. The two marketed KRAS switch-II inhibitors both break the
// default 500 Da rule-of-five ceiling — sotorasib is 560.6 Da, adagrasib 604.1 — so
// the curated site's weight window has to reach the pre-filter, or the generation
// prompt asks for 430–620 Da while everything above 500 Da is silently dropped.
//
// The tool compounds ARS-1620 (430.8) and ARS-853 (433.0) sit under the default
// ceiling and survive it. Saying otherwise takes a molecule that is not ARS-1620.
func TestCuratedThresholdsAdmitTheRealG12CInhibitors(t *testing.T) {
	skipUnlessValidator(t)

	// Divarasib is 622.1 Da — 2.1 Da above our own MaxMW. It is excluded here so the
	// suite reflects what the window actually admits; TestDivarasibFallsOutsideTheWindow
	// pins that gap so widening the window cannot pass unnoticed.
	heavy := map[string]bool{"sotorasib": true, "adagrasib": true}
	var names, batch []string
	for _, c := range priorArt(t) {
		if c.Name == "divarasib" {
			continue
		}
		names = append(names, c.Name)
		batch = append(batch, c.SMILES)
	}

	// Default thresholds: the two drugs above 500 Da are discarded.
	base, err := ValidateSMILES(context.Background(), "run_defaults", batch, nil, nil)
	if err != nil {
		t.Fatalf("ValidateSMILES(defaults): %v", err)
	}
	for i, v := range base {
		if heavy[names[i]] && v.Kept {
			t.Errorf("%s survived the default pre-filter; the 500 Da ceiling was expected to drop it", names[i])
		}
		if !heavy[names[i]] && !v.Kept && v.DropReason == "mw_out_of_range" {
			t.Errorf("%s dropped for weight by the default pre-filter; it is under 500 Da", names[i])
		}
	}

	// The curated KRAS G12C window admits them.
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

// The design window tops out at 620 Da and divarasib weighs 622.1, so the newest
// clinical G12C inhibitor is one the generator is forbidden to propose. That may be
// the right trade — every extra dalton widens the search — but it must be a decision
// rather than an accident, so it is pinned. If the window moves, this test fails and
// whoever moved it has to say so out loud.
func TestDivarasibFallsOutsideTheWindow(t *testing.T) {
	skipUnlessValidator(t)

	var divarasib priorArtCompound
	for _, c := range priorArt(t) {
		if c.Name == "divarasib" {
			divarasib = c
		}
	}
	if divarasib.SMILES == "" {
		t.Fatal("divarasib missing from the prior art file")
	}

	site := LookupKnownSite("P01116", models.Mutation{WildType: "G", Position: 12, Mutant: "C"}, "")
	th := siteThresholds(site)
	if th == nil {
		t.Fatal("the curated switch-II site declares no weight window")
	}
	got, err := ValidateSMILES(context.Background(), "run_divarasib", []string{divarasib.SMILES}, nil, th)
	if err != nil {
		t.Fatalf("ValidateSMILES: %v", err)
	}
	if got[0].Kept || got[0].DropReason != "mw_out_of_range" {
		t.Errorf("divarasib (%.1f Da) kept=%v drop=%q; the window is %.0f–%.0f Da. "+
			"If the window was widened on purpose, update this test and TestCuratedThresholdsAdmitTheRealG12CInhibitors.",
			divarasib.MW, got[0].Kept, got[0].DropReason, th.MWMin, th.MWMax)
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
