package services

import "testing"

// A hand-written extraction shaped like a real one for the EGFR C797S case. The point
// of the fixture is the trap the prompt warns about: the mutation is C797S, but the
// reactive residue a warhead should bond is Cys775 — the two must not collapse into
// one — and every field carries its verbatim source sentence under its JSON key.
const egfrExtractionJSON = `{
  "uniprot_id": "P00533",
  "protein_name": "Epidermal growth factor receptor",
  "mutation": "C797S",
  "reactive_residue": "Cys775",
  "covalent": true,
  "mechanism": "The C797S mutation removes the cysteine that osimertinib covalently targets; selectivity is instead recovered by a warhead that bonds Cys775.",
  "pharmacophore": "A pyrimidine core bearing a reversible hinge binder and a short acrylamide linker.",
  "min_mw": 430,
  "max_mw": 620,
  "prior_art": ["osimertinib", "brigatinib"],
  "pocket_residues": [790, 797, 775],
  "pdb_id": "6OIM",
  "chain": "A",
  "citations": {
    "reactive_residue": "Because C797S abolishes the osimertinib anchor, our fourth-generation inhibitors were designed to form a covalent bond with Cys775.",
    "min_mw": "Active analogues clustered in a molecular-weight window of 430 to 620 Da.",
    "pdb_id": "The mutant kinase domain was modelled on the holo structure PDB 6OIM."
  },
  "notes": "The weight window is inferred from the active series; confirm against Table 2."
}`

func TestParseExtraction(t *testing.T) {
	site, err := parseExtraction([]byte(egfrExtractionJSON))
	if err != nil {
		t.Fatalf("parseExtraction returned error on valid JSON: %v", err)
	}

	// The reactive residue must survive the round-trip as Cys775, NOT the mutation
	// site — this is the field the whole extractor exists to get right.
	if site.ReactiveResidue != "Cys775" {
		t.Errorf("ReactiveResidue = %q, want %q", site.ReactiveResidue, "Cys775")
	}
	if site.Mutation != "C797S" {
		t.Errorf("Mutation = %q, want %q", site.Mutation, "C797S")
	}
	if !site.Covalent {
		t.Errorf("Covalent = false, want true")
	}
	if site.UniprotID != "P00533" {
		t.Errorf("UniprotID = %q, want %q", site.UniprotID, "P00533")
	}
	if site.MinMW != 430 || site.MaxMW != 620 {
		t.Errorf("weight window = %v–%v, want 430–620", site.MinMW, site.MaxMW)
	}
	if site.PDBID != "6OIM" {
		t.Errorf("PDBID = %q, want %q", site.PDBID, "6OIM")
	}
	if len(site.PriorArt) != 2 {
		t.Errorf("PriorArt = %v, want 2 entries", site.PriorArt)
	}

	// The provenance is the reason the type exists: the reactive residue must ship
	// beside the sentence it was drawn from.
	cite, ok := site.Citations["reactive_residue"]
	if !ok || cite == "" {
		t.Errorf("Citations[\"reactive_residue\"] is missing or empty; got %q (present=%v)", cite, ok)
	}
}

func TestParseExtractionMalformed(t *testing.T) {
	if _, err := parseExtraction([]byte(`{"covalent": true, "prior_art": [`)); err == nil {
		t.Errorf("parseExtraction accepted malformed JSON, want an error")
	}
}
