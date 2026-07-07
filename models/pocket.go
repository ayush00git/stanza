package models

// Pocket represents a single druggable pocket identified by fpocket.
type Pocket struct {
	PocketID          int        `json:"pocket_id"`
	Score             float64    `json:"druggability_score"`
	Volume            float64    `json:"volume"`
	SurfaceArea       float64    `json:"surface_area"`  // Total SASA (Å²)
	Depth             float64    `json:"depth"`         // Mean alpha-sphere radius proxy (Å)
	Hydrophobicity    float64    `json:"hydrophobicity"`
	Polarity          float64    `json:"polarity"`
	SourceType        string     `json:"source_type"` // "monomer" or "dimer"
	IsInterfacePocket bool       `json:"is_interface_pocket"`
	IsConserved       bool       `json:"is_conserved,omitempty"`
	IsEmergent        bool       `json:"is_emergent,omitempty"`
	AvgDelta          float64    `json:"avg_disorder_delta"`
	AvgPLDDT          float64    `json:"avg_plddt"`
	ResidueIndices    []int              `json:"residue_indices"`
	ResidueNames      []string           `json:"residue_names"`
	ResidueChains     []string           `json:"residue_chains"`
	Chains            []string           `json:"chains,omitempty"`
	Center            [3]float64         `json:"center"`
	Fragments         []Fragment         `json:"fragments,omitempty"`
	LocalPLDDT        map[string]float64 `json:"-"` // Keyed by Chain:ResIdx
	ResidueConfidences []ResidueConfidence `json:"residue_confidences"`
}

// Fragment represents a suggested small molecule from ChEMBL.
type Fragment struct {
	ChemblID   string  `json:"chembl_id"`
	Name       string  `json:"name"`
	SMILES     string  `json:"smiles"`
	MolWeight  float64 `json:"mol_weight"`
	LogP       float64 `json:"logp"`
	Similarity float64 `json:"similarity_score"`
}

// BindingSiteResult is the full response for the binding-sites endpoint.
type BindingSiteResult struct {
	UniprotID      string   `json:"uniprot_id"`
	ComplexEntryID string   `json:"complex_entry_id"`
	TotalPockets   int      `json:"total_pockets"`
	InterfaceCount int      `json:"interface_pocket_count"`
	Pockets        []Pocket `json:"pockets"`
	MonomerTotalPockets int               `json:"monomer_total_pockets"`
	MonomerPockets      []Pocket          `json:"monomer_pockets"`
	Comparison          *ComparisonResult `json:"comparison,omitempty"`
}

// ResidueConfidence holds per-residue pLDDT data for both monomer and dimer forms.
type ResidueConfidence struct {
	ResidueIndex int     `json:"residue_index"`
	Chain        string  `json:"chain"`
	MonomerPLDDT float64 `json:"monomer_plddt"`
	DimerPLDDT   float64 `json:"dimer_plddt"`
	Delta        float64 `json:"delta"`
}
