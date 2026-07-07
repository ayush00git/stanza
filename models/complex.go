package models

// Complex represents a protein complex with its metadata.
// Use -1 for unknown drug counts.
type Complex struct {
	AlphafoldID      string   `json:"alphafold_id"`
	UniprotID        string   `json:"uniprot_id"`
	ProteinName      string   `json:"protein_name"`
	GeneName         string   `json:"gene_name"`
	Organism         string   `json:"organism"`
	OrganismID       int      `json:"organism_id"`
	IsWHOPathogen    bool     `json:"is_who_pathogen"`
	DiseaseAssoc     []string `json:"disease_associations"`
	MonomerPLDDTAvg  float64  `json:"monomer_plddt_avg"`
	DimerPLDDTAvg    float64  `json:"dimer_plddt_avg"`
	DisorderDelta    float64  `json:"disorder_delta"`
	DrugCount        int      `json:"drug_count"`
	KnownDrugNames   []string `json:"known_drug_names"`
	MonomerStructURL string   `json:"monomer_structure_url"`
	ComplexStructURL string   `json:"complex_structure_url"`
	Category         string   `json:"category"`
	ReviewStatus     string   `json:"review_status"` // "reviewed" (Swiss-Prot) or "unreviewed" (TrEMBL)
}

// SearchResult wraps the search response with metadata.
type SearchResult struct {
	Query   string    `json:"query"`
	Count   int       `json:"count"`
	Source  string    `json:"source"` // "live" or "fallback"
	Results []Complex `json:"results"`
}
