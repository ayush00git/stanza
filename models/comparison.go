package models

// ComparisonSummaryMetrics holds top-level counts and averages
type ComparisonSummaryMetrics struct {
	TotalMonomerPockets   int     `json:"total_monomer_pockets"`
	TotalDimerPockets     int     `json:"total_dimer_pockets"`
	InterfacePocketCount  int     `json:"interface_pocket_count"`
	AvgMonomerDruggability float64 `json:"avg_monomer_druggability"`
	AvgDimerDruggability   float64 `json:"avg_dimer_druggability"`
}

// PocketMapping tracks how a pocket changed
type PocketMapping struct {
	Conserved   int `json:"conserved_count"`
	MonomerOnly int `json:"monomer_only_count"`
	Emergent    int `json:"emergent_count"`
	Interface   int `json:"interface_count"`
}

// GraphDatasets holds dataset arrays for recharts
type GraphDatasets struct {
	PocketCounts           []CountData      `json:"pocket_counts"`
	DruggabilityDistMonomer []float64        `json:"druggability_dist_monomer"`
	DruggabilityDistDimer   []float64        `json:"druggability_dist_dimer"`
	StabilizationScatter    []ScatterData    `json:"stabilization_scatter"`
}

type CountData struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type ScatterData struct {
	PocketID         int     `json:"pocket_id"`
	AvgDelta         float64 `json:"avg_delta"`
	DruggabilityScore float64 `json:"druggability_score"`
}

// PropertyChanges holds averages across pockets
type PropertyChanges struct {
	MonomerAvgVolume         float64 `json:"monomer_avg_volume"`
	DimerAvgVolume           float64 `json:"dimer_avg_volume"`
	MonomerAvgHydrophobicity float64 `json:"monomer_avg_hydrophobicity"`
	DimerAvgHydrophobicity   float64 `json:"dimer_avg_hydrophobicity"`
	MonomerAvgPolarity       float64 `json:"monomer_avg_polarity"`
	DimerAvgPolarity         float64 `json:"dimer_avg_polarity"`
}

// StabilizationStats tracks residue level improvements
type StabilizationStats struct {
	ResiduesWithPositiveDelta int     `json:"residues_with_positive_delta"`
	ResiduesInInterfacePockets int     `json:"residues_in_interface_pockets"`
	EnrichmentScore           float64 `json:"enrichment_score"`
}

// FragmentComparison identifies unique fragments
type FragmentComparison struct {
	UniqueDimerFragments     []Fragment `json:"unique_dimer_fragments"`
	UniqueInterfaceFragments []Fragment `json:"unique_interface_fragments"`
}

// ComparisonResult is the root object returned for the comparison module
type ComparisonResult struct {
	SummaryMetrics     ComparisonSummaryMetrics `json:"summary_metrics"`
	DDGI               float64                  `json:"ddgi"`
	PocketMapping      PocketMapping            `json:"pocket_mapping"`
	InterfacePockets   []Pocket                 `json:"interface_pockets"`
	ConservedPockets   []Pocket                 `json:"conserved_pockets"`
	EmergentPockets    []Pocket                 `json:"emergent_pockets"`
	GraphDatasets      GraphDatasets            `json:"graph_datasets"`
	PropertyChanges    PropertyChanges          `json:"property_changes"`
	StabilizationStats StabilizationStats       `json:"stabilization_stats"`
	FragmentComparison FragmentComparison       `json:"fragment_comparison"`
}
