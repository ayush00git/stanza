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
	MutantScore float64 `json:"mutant_score"` // raw mutant Vina affinity; never covalent-adjusted
	Selectivity float64 `json:"selectivity"`  // wt_score - mutant_score; large positive spares WT
	WTPosePDB   string  `json:"wt_pose_pdb,omitempty"`
	MutantPosePDB string `json:"mutant_pose_pdb,omitempty"`
	// Covalent is set whenever a warhead-bearing molecule is docked against a mutated
	// cysteine, whatever the outcome — see the Covalent* status constants. It never
	// alters MutantScore: the covalent bond is not a Vina energy and folding a
	// constant into the affinity would make Selectivity a restatement of that
	// constant. MutantPosePDB is the tethered complex only when Status is
	// CovalentTethered. nil for non-covalent molecules and non-cysteine targets.
	Covalent *CovalentDock `json:"covalent,omitempty"`
}

// Covalent assessment outcomes. A warhead that cannot reach the thiol and a warhead
// whose measurement failed are different facts, and both differ from a molecule that
// simply carries no warhead — reporting all three as "not covalent" is what let a
// broken reach measurement look like an honest negative.
const (
	CovalentTethered     = "tethered"        // geometry permits the bond; a valid adduct pose was built
	CovalentFeasible     = "feasible"        // geometry permits the bond; the adduct pose was rejected
	CovalentInfeasible   = "infeasible"      // warhead present but cannot attack the thiol
	CovalentUnreadable   = "unreadable_pose" // no docked mode could be mapped onto the ligand
	CovalentAssessFailed = "assess_failed"   // the assessment itself errored
	CovalentNoThiol      = "no_thiol"        // the target residue carries no SG
)

// CovalentDock records whether a warhead can actually attack the mutated cysteine.
//
// It deliberately carries NO energy. Vina scores non-covalently and cannot see the
// bond that creates a covalent inhibitor's selectivity; the previous model bolted a
// constant "credit" (4.0 kcal/mol) onto the mutant affinity to stand in for it. That
// was wrong three ways: covalent potency is kinetic (kinact/KI, spanning 76 →
// 35,000 M⁻¹s⁻¹ from ARS-853 to adagrasib, which one constant cannot separate); the
// wild type has no thiol at all, so the discrimination is unbounded rather than a
// few kcal/mol; and since the WT and mutant non-covalent scores agree to ~0.1
// kcal/mol, "selectivity" collapsed into a restatement of the constant.
//
// What IS measurable from a docked pose is geometry: can the warhead's electrophilic
// carbon reach the thiol, along a trajectory that permits nucleophilic attack, in a
// pose the receptor actually binds? Feasibility reports exactly that, and nothing
// more. It is dimensionless on purpose.
type CovalentDock struct {
	TargetResidue string  `json:"target_residue"`           // e.g. "Cys12"
	WarheadType   string  `json:"warhead_type,omitempty"`   // e.g. "acrylamide"
	Status        string  `json:"status"`                   // one of the Covalent* constants
	Feasibility   float64 `json:"feasibility"`              // 0–1; 0 = the warhead cannot attack the thiol
	ReachDistance float64 `json:"reach_distance,omitempty"` // MEDIAN warhead-C → thiol-SG over replicates (Å)
	ReachSpread   float64 `json:"reach_spread,omitempty"`   // max − min reach across replicates (Å)
	AttackAngle   float64 `json:"attack_angle,omitempty"`   // approach angle at the electrophilic carbon (degrees)
	ModeRank      int     `json:"mode_rank,omitempty"`      // 1-based Vina mode the geometry came from
	ModeAffinity  float64 `json:"mode_affinity,omitempty"`  // that mode's Vina affinity (kcal/mol)
	Replicates    int     `json:"replicates,omitempty"`     // docking seeds the geometry was measured over
	BondDistance  float64 `json:"bond_distance,omitempty"`  // S–C of the emitted tether pose (Å)
	// Uncertain marks a molecule whose covalent call flips with the docking seed:
	// some replicates place the warhead where it can attack and others do not. Such a
	// molecule is not "better" or "worse" than its neighbours; it is indistinguishable,
	// and ranking it on a median would launder noise into signal.
	Uncertain bool   `json:"uncertain,omitempty"`
	Note      string `json:"note,omitempty"` // why a tether or an assessment failed
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
