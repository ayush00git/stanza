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

// TestRegisterExtractedSiteCovalentResidue confirms a covalent site whose reactive residue
// differs from the mutation site carries that residue across to the KnownSite. This is the
// EGFR C797S case: the mutation removes Cys797, so the design bonds Cys775 — and without the
// carry-through the generator would read the mutant residue (Ser), call the target
// non-covalent, and design reversible molecules.
func TestRegisterExtractedSiteCovalentResidue(t *testing.T) {
	RegisterExtractedSite(&models.ExtractedSite{
		UniprotID:       "P00533",
		ProteinName:     "Epidermal growth factor receptor",
		Mutation:        "C797S",
		ReactiveResidue: "Cys775",
		Covalent:        true,
	})

	mut := models.Mutation{WildType: "C", Position: 797, Mutant: "S"}
	got := LookupKnownSite("P00533", mut, "")
	if got == nil {
		t.Fatal("LookupKnownSite returned nil for a registered covalent site")
	}
	if got.CovalentResidue != "Cys775" {
		t.Errorf("CovalentResidue = %q, want %q", got.CovalentResidue, "Cys775")
	}
}

// TestRegisterExtractedSiteNonCovalentNoResidue confirms the reactive residue is carried
// ONLY when the paper says the target is covalent. A named residue on a non-covalent site
// must not trip the warhead brief.
func TestRegisterExtractedSiteNonCovalentNoResidue(t *testing.T) {
	RegisterExtractedSite(&models.ExtractedSite{
		UniprotID:       "Q99999",
		ProteinName:     "Test kinase",
		Mutation:        "A123T",
		ReactiveResidue: "Cys200",
		Covalent:        false,
	})

	mut := models.Mutation{WildType: "A", Position: 123, Mutant: "T"}
	got := LookupKnownSite("Q99999", mut, "")
	if got == nil {
		t.Fatal("LookupKnownSite returned nil for a registered site")
	}
	if got.CovalentResidue != "" {
		t.Errorf("CovalentResidue = %q, want empty (site is non-covalent)", got.CovalentResidue)
	}
}
