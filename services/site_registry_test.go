package services

import (
	"testing"

	"github.com/ayush00git/stanza/models"
)

// TestRegisterExtractedSite confirms a registered paper extraction is found by
// LookupKnownSite for its exact uniprot+mutation, with guidance and template mapped
// across from the ExtractedSite.
func TestRegisterExtractedSite(t *testing.T) {
	RegisterExtractedSite(&models.ExtractedSite{
		UniprotID:      "P00533",
		ProteinName:    "Epidermal growth factor receptor",
		Mutation:       "C797S",
		Mechanism:      "covalent",
		Pharmacophore:  "acrylamide warhead",
		MinMW:          400,
		MaxMW:          550,
		PriorArt:       []string{"osimertinib"},
		PocketResidues: []int{718, 719, 797},
		PDBID:          "6OIM",
		Chain:          "A",
	})

	mut := models.Mutation{WildType: "C", Position: 797, Mutant: "S"}
	got := LookupKnownSite("P00533", mut, "")
	if got == nil {
		t.Fatal("LookupKnownSite returned nil for a registered site")
	}
	if got.Guidance == nil {
		t.Fatal("registered site has nil Guidance")
	}
	if got.Guidance.MinMW != 400 {
		t.Errorf("Guidance.MinMW = %v, want 400", got.Guidance.MinMW)
	}
	if got.Template == nil {
		t.Fatal("registered site has nil Template")
	}
	if got.Template.PDBID != "6OIM" {
		t.Errorf("Template.PDBID = %q, want %q", got.Template.PDBID, "6OIM")
	}
}
