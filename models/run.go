package models

import "strconv"

// Mutation is a parsed point substitution, e.g. "G12C".
type Mutation struct {
	Raw      string `json:"raw"`
	WildType string `json:"wild_type"` // single uppercase amino-acid letter
	Position int    `json:"position"`  // 1-based, UniProt canonical numbering
	Mutant   string `json:"mutant"`    // single uppercase amino-acid letter
}

// String returns the raw "G12C" form assembled from WildType, Position and Mutant.
func (m Mutation) String() string {
	return m.WildType + strconv.Itoa(m.Position) + m.Mutant
}

// WTStructureSource records where a run's wild-type structure came from.
type WTStructureSource string

const (
	SourcePDBHolo   WTStructureSource = "pdb_holo"
	SourcePDBApo    WTStructureSource = "pdb_apo"
	SourceAlphaFold WTStructureSource = "alphafold"
)

// WTStructure is the wild-type structure chosen for a run (Stage 1 output).
type WTStructure struct {
	Source          WTStructureSource `json:"source"`
	PDBID           string            `json:"pdb_id,omitempty"`
	AlphafoldID     string            `json:"alphafold_id,omitempty"`
	StructureURL    string            `json:"structure_url"`
	Chain           string            `json:"chain,omitempty"`
	LigandCount     int               `json:"ligand_count"`
	Resolution      float64           `json:"resolution,omitempty"`
	ResidueResolved bool              `json:"residue_resolved"`  // mutated position present in this structure
	WildTypeMatches bool              `json:"wild_type_matches"` // residue at position == Mutation.WildType
	Notes           []string          `json:"notes,omitempty"`
}

// Run is a resistance-design run. Stage 1 populates WTStructure.
type Run struct {
	ID          string       `json:"id"`
	UniprotID   string       `json:"uniprot_id"`
	Mutation    Mutation     `json:"mutation"`
	SiteHint    string       `json:"site_hint,omitempty"`
	Status      string       `json:"status"` // "structure_acquired" | "error"
	WTStructure *WTStructure `json:"wt_structure,omitempty"`
	Error       string       `json:"error,omitempty"`
	CreatedAt   string       `json:"created_at"`
}
