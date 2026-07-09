package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ayush00git/stanza/models"
)

// testStore connects to the Postgres pointed at by STANZA_TEST_DATABASE_URL and
// applies the migrations. It skips the test when the variable is unset, so the
// default `go test ./...` never requires a live database.
func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	url := envOrSkip(t)
	ctx := context.Background()
	s, err := New(ctx, url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s, ctx
}

func envOrSkip(t *testing.T) string {
	t.Helper()
	url := os.Getenv("STANZA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set STANZA_TEST_DATABASE_URL to run store integration tests")
	}
	return url
}

func TestProfileRoundTrip(t *testing.T) {
	s, ctx := testStore(t)

	p := &models.Profile{Name: "Ada Lovelace", Email: "ada@example.org", Institution: "Analytical Society", Field: "oncology"}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	t.Cleanup(func() { _, _ = s.Pool.Exec(ctx, "DELETE FROM profiles WHERE id=$1", p.ID) })

	if p.ID == "" {
		t.Fatal("CreateProfile did not set an ID")
	}
	if p.CreatedAt == "" {
		t.Fatal("CreateProfile did not set CreatedAt")
	}

	got, ok, err := s.GetProfile(ctx, p.ID)
	if err != nil || !ok {
		t.Fatalf("GetProfile: ok=%v err=%v", ok, err)
	}
	if got.Name != p.Name || got.Email != p.Email || got.Institution != p.Institution || got.Field != p.Field {
		t.Fatalf("GetProfile mismatch: %+v vs %+v", got, p)
	}

	list, err := s.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if !containsProfile(list, p.ID) {
		t.Fatalf("ListProfiles missing %s", p.ID)
	}

	if _, ok, _ := s.GetProfile(ctx, uuid.NewString()); ok {
		t.Fatal("GetProfile returned ok for a nonexistent id")
	}
}

func TestRunRoundTrip(t *testing.T) {
	s, ctx := testStore(t)

	prof := &models.Profile{Name: "Rosalind Franklin"}
	if err := s.CreateProfile(ctx, prof); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	t.Cleanup(func() { _, _ = s.Pool.Exec(ctx, "DELETE FROM profiles WHERE id=$1", prof.ID) })

	sa := 3.2
	run := &models.Run{
		ID:        uuid.NewString(),
		ProfileID: prof.ID,
		UniprotID: "P01116",
		Mutation:  models.Mutation{Raw: "G12C", WildType: "G", Position: 12, Mutant: "C"},
		SiteHint:  "switch-II",
		Status:    "mutant_built",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		WTStructure: &models.WTStructure{
			Source: models.SourceAlphaFold, StructureURL: "https://example/af.pdb", LigandCount: 1, ResidueResolved: true,
		},
		Mutagenesis: &models.MutagenesisResult{
			Tool: "pdbfixer", TargetChain: "A", TargetResidueNum: 12, WildTypeResidue: "GLY", MutantResidue: "CYS",
		},
		Pockets: &models.PocketAnalysis{
			EmergentCount: 1,
			MutantPockets: []models.Pocket{{PocketID: 1, Volume: 120, Hydrophobicity: 0.4}},
		},
		Candidates: []models.Candidate{
			{SMILES: "CCO", InChIKey: "KEY1", QED: 0.7, RO5Pass: true, SAScore: &sa, MolWeight: 46.07, LogP: -0.14},
		},
		Docks: []models.LigandDock{
			{SMILES: "CCO", WTScore: -6.1, MutantScore: -7.5, Selectivity: 1.4, WTPosePDB: "WTPOSE", MutantPosePDB: "MUTPOSE"},
		},
	}
	t.Cleanup(func() { _, _ = s.Pool.Exec(ctx, "DELETE FROM runs WHERE id=$1", run.ID) })

	if err := s.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	got, ok, err := s.GetRun(ctx, run.ID)
	if err != nil || !ok {
		t.Fatalf("GetRun: ok=%v err=%v", ok, err)
	}
	if got.ProfileID != prof.ID {
		t.Fatalf("ProfileID: got %q want %q", got.ProfileID, prof.ID)
	}
	if got.UniprotID != "P01116" || got.SiteHint != "switch-II" || got.Status != "mutant_built" {
		t.Fatalf("header mismatch: %+v", got)
	}
	if got.Mutation != run.Mutation {
		t.Fatalf("mutation mismatch: %+v vs %+v", got.Mutation, run.Mutation)
	}
	if got.WTStructure == nil || got.WTStructure.Source != models.SourceAlphaFold {
		t.Fatalf("wt_structure not round-tripped: %+v", got.WTStructure)
	}
	if got.Mutagenesis == nil || got.Mutagenesis.MutantResidue != "CYS" {
		t.Fatalf("mutagenesis not round-tripped: %+v", got.Mutagenesis)
	}
	if got.Pockets == nil || got.Pockets.EmergentCount != 1 || len(got.Pockets.MutantPockets) != 1 {
		t.Fatalf("pockets not round-tripped: %+v", got.Pockets)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].InChIKey != "KEY1" || got.Candidates[0].SAScore == nil || *got.Candidates[0].SAScore != sa {
		t.Fatalf("candidates not round-tripped: %+v", got.Candidates)
	}
	if len(got.Docks) != 1 || got.Docks[0].MutantPosePDB != "MUTPOSE" || got.Docks[0].Selectivity != 1.4 {
		t.Fatalf("docks not round-tripped: %+v", got.Docks)
	}

	// Re-save (upsert) must not duplicate children.
	if err := s.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun (re-save): %v", err)
	}
	got2, _, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun after re-save: %v", err)
	}
	if len(got2.Candidates) != 1 || len(got2.Docks) != 1 {
		t.Fatalf("re-save duplicated children: %d candidates, %d docks", len(got2.Candidates), len(got2.Docks))
	}

	// Profile scoping.
	all, err := s.ListRuns(ctx, "")
	if err != nil {
		t.Fatalf("ListRuns(all): %v", err)
	}
	if !containsRun(all, run.ID) {
		t.Fatal("ListRuns(all) missing the run")
	}
	mine, err := s.ListRuns(ctx, prof.ID)
	if err != nil {
		t.Fatalf("ListRuns(profile): %v", err)
	}
	if !containsRun(mine, run.ID) {
		t.Fatal("ListRuns(profile) missing the run")
	}
	other, err := s.ListRuns(ctx, uuid.NewString())
	if err != nil {
		t.Fatalf("ListRuns(other): %v", err)
	}
	if containsRun(other, run.ID) {
		t.Fatal("ListRuns(other profile) unexpectedly returned the run")
	}
}

func TestAnonymousRun(t *testing.T) {
	s, ctx := testStore(t)
	run := &models.Run{
		ID: uuid.NewString(), UniprotID: "P00533",
		Mutation:  models.Mutation{Raw: "T790M", WildType: "T", Position: 790, Mutant: "M"},
		Status:    "structure_acquired",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	t.Cleanup(func() { _, _ = s.Pool.Exec(ctx, "DELETE FROM runs WHERE id=$1", run.ID) })
	if err := s.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun (anonymous): %v", err)
	}
	got, ok, err := s.GetRun(ctx, run.ID)
	if err != nil || !ok {
		t.Fatalf("GetRun: ok=%v err=%v", ok, err)
	}
	if got.ProfileID != "" {
		t.Fatalf("anonymous run should have empty ProfileID, got %q", got.ProfileID)
	}
}

func containsProfile(ps []*models.Profile, id string) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}

func containsRun(rs []*models.Run, id string) bool {
	for _, r := range rs {
		if r.ID == id {
			return true
		}
	}
	return false
}
