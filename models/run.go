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
	ResidueResolved bool              `json:"residue_resolved"`             // mutated position present in this structure
	WildTypeMatches bool              `json:"wild_type_matches"`            // residue at position == Mutation.WildType
	TargetChain     string            `json:"target_chain,omitempty"`       // auth chain of the mutated residue in this structure
	TargetAuthSeqID int               `json:"target_auth_seq_id,omitempty"` // auth residue number of the mutated residue in this structure
	Notes           []string          `json:"notes,omitempty"`
}

// MutagenesisResult is the Stage-2 output: a matched WT/mutant structure pair
// built from the acquired base structure by side-chain mutagenesis.
type MutagenesisResult struct {
	Tool               string   `json:"tool"`                 // e.g. "pdbfixer"
	WTStructureURL     string   `json:"wt_structure_url"`     // served WT-normalized structure
	MutantStructureURL string   `json:"mutant_structure_url"` // served mutant structure
	TargetChain        string   `json:"target_chain"`
	TargetResidueNum   int      `json:"target_residue_number"`
	WildTypeResidue    string   `json:"wild_type_residue"` // 3-letter, e.g. "GLY"
	MutantResidue      string   `json:"mutant_residue"`    // 3-letter, e.g. "CYS"
	Notes              []string `json:"notes,omitempty"`
}

// PocketAnalysis is the Stage-3 result for a run: pockets detected on both tracks,
// their cross-track match counts, and the resistance-pocket context for the loop.
type PocketAnalysis struct {
	WTPockets      []Pocket             `json:"wt_pockets"`
	MutantPockets  []Pocket             `json:"mutant_pockets"`
	ConservedCount int                  `json:"conserved_count"`
	WTOnlyCount    int                  `json:"wt_only_count"`
	EmergentCount  int                  `json:"emergent_count"`
	Context        *MutantPocketContext `json:"context,omitempty"`
}

// LigandDock is one molecule docked into both tracks of a run (Stage 4): the
// resistance pocket of the WT structure and of the mutant structure.
type LigandDock struct {
	SMILES      string  `json:"smiles"`
	WTScore     float64 `json:"wt_score"`     // Vina affinity (kcal/mol); more negative = stronger
	MutantScore float64 `json:"mutant_score"` // mutant affinity; covalent-adjusted when Covalent != nil
	Selectivity float64 `json:"selectivity"`  // wt_score - mutant_score; large positive spares WT
	WTPosePDB   string  `json:"wt_pose_pdb,omitempty"`
	MutantPosePDB string `json:"mutant_pose_pdb,omitempty"`
	// Covalent is set when the mutant track modelled a covalent tether to the mutated
	// cysteine; MutantScore then includes the covalent credit and MutantPosePDB is the
	// tethered complex. nil for non-covalent molecules or non-cysteine targets.
	Covalent *CovalentDock `json:"covalent,omitempty"`
}

// CovalentDock records the covalent-tether model applied to the mutant track. Vina
// scores non-covalently, so the WT/mutant selectivity of a covalent warhead is
// invisible to it; this captures the geometry that recovers it — whether the
// warhead reaches the cysteine thiol — and the credit that models the bond only the
// mutant can form.
type CovalentDock struct {
	TargetResidue    string  `json:"target_residue"`     // e.g. "Cys12"
	WarheadType      string  `json:"warhead_type"`       // e.g. "acrylamide"
	ReachDistance    float64 `json:"reach_distance"`     // best warhead-C → thiol-SG across modes (Å)
	Credit           float64 `json:"credit"`             // covalent credit applied to the mutant score (kcal/mol)
	NonCovalentScore float64 `json:"non_covalent_score"` // raw Vina mutant affinity before the credit
	BondDistance     float64 `json:"bond_distance,omitempty"` // S–C of the emitted tether pose (Å)
}

// Candidate is a Stage-6 molecule proposed by Claude that passed the Stage-5 RDKit
// pre-filter (valid, unique, drug-like), awaiting on-demand docking. SMILES is the
// RDKit canonical form; the drug-likeness numbers are surfaced in the UI and feed
// selectivity ranking. SAScore is nil when the optional SA scorer is unavailable.
type Candidate struct {
	SMILES    string   `json:"smiles"`
	InChIKey  string   `json:"inchikey"`
	QED       float64  `json:"qed"`
	RO5Pass   bool     `json:"ro5_pass"`
	SAScore   *float64 `json:"sa_score,omitempty"`
	MolWeight float64  `json:"mol_weight"`
	LogP      float64  `json:"logp"`
}

// Run is a resistance-design run. Stage 1 populates WTStructure.
type Run struct {
	ID          string             `json:"id"`
	ProfileID   string             `json:"profile_id,omitempty"` // owning researcher profile (Stage 8); "" if anonymous
	UniprotID   string             `json:"uniprot_id"`
	Mutation    Mutation           `json:"mutation"`
	SiteHint    string             `json:"site_hint,omitempty"`
	Status      string             `json:"status"` // "structure_acquired" | "error"
	WTStructure *WTStructure       `json:"wt_structure,omitempty"`
	Mutagenesis *MutagenesisResult `json:"mutagenesis,omitempty"`
	Pockets     *PocketAnalysis    `json:"pockets,omitempty"`
	Docks       []LigandDock       `json:"docks,omitempty"`
	Candidates  []Candidate        `json:"candidates,omitempty"` // Stage-6 proposals that passed Stage-5 validation, awaiting on-demand docking
	Error       string             `json:"error,omitempty"`
	CreatedAt   string             `json:"created_at"`
}
