package models

// ExtractedSite is a curated-site draft that Claude pulled from a paper, with every
// field carrying the sentence it was drawn from. It is the front-of-pipeline analogue of
// a hand-curated entry in services/known_sites.go: it maps onto a SiteGuidance plus a
// SiteTemplate once a human confirms it.
//
// The provenance is the whole point, and the reason this type exists rather than the code
// writing straight into SiteGuidance. An extracted number drives docking, generation, and
// the weight gate downstream; if the model misreads "residue 797" as "790" the pipeline
// runs confidently on a wrong anchor. So the model proposes and a person ratifies: nothing
// here touches a run until it is confirmed, and every field ships beside the exact text it
// came from (Citations) so that confirmation is a check, not an act of faith.
type ExtractedSite struct {
	// Target identity.
	UniprotID   string `json:"uniprot_id"`   // e.g. "P00533" (EGFR); "" if the paper does not name one
	ProteinName string `json:"protein_name"` // e.g. "Epidermal growth factor receptor"
	Mutation    string `json:"mutation"`     // e.g. "C797S"; "" if the target is a wild-type residue
	// ReactiveResidue is the residue a covalent warhead should bond, e.g. "Cys797". It may
	// differ from the mutation site: EGFR C797S destroys the osimertinib anchor, and the
	// design instead targets Cys775. "" for a non-covalent target.
	ReactiveResidue string `json:"reactive_residue"`
	Covalent        bool   `json:"covalent"`

	// Design guidance — maps onto services.SiteGuidance.
	Mechanism      string   `json:"mechanism"`       // where selectivity really comes from, one paragraph
	Pharmacophore  string   `json:"pharmacophore"`   // the substructure that drives potency here
	MinMW          float64  `json:"min_mw"`          // weight window floor, Da
	MaxMW          float64  `json:"max_mw"`          // weight window ceiling, Da
	PriorArt       []string `json:"prior_art"`       // published inhibitors the generator must not re-derive
	PocketResidues []int    `json:"pocket_residues"` // UniProt-numbered residues lining the pocket

	// Structure — maps onto services.SiteTemplate. The PDB the WT/mutant pair is built on.
	PDBID string `json:"pdb_id"` // e.g. "6OIM"; "" if the paper names no usable holo structure
	Chain string `json:"chain"`  // e.g. "A"

	// Citations is one verbatim source sentence per field above, keyed by the JSON field
	// name (e.g. "reactive_residue", "min_mw", "pdb_id"). A field the model could not ground
	// in the paper is left out of this map rather than cited to nothing.
	Citations map[string]string `json:"citations"`

	// Notes carries the model's own flags: which fields it was unsure of, what the paper did
	// not state, what the human should double-check. It is guidance for the confirm step, not
	// a value that drives anything.
	Notes string `json:"notes"`
}
