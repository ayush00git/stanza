package services

import (
	"strings"
	"testing"

	"github.com/ayush00git/stanza/models"
)

func testPocketContext() *models.MutantPocketContext {
	return &models.MutantPocketContext{
		MutantPocket: models.MutantPocket{
			KeyResidues:    []string{"Cys12", "Gln61", "His95", "Tyr96"},
			Volume:         438,
			Hydrophobicity: 4.6,
		},
		PocketDelta: models.PocketDelta{Changed: []string{"Gly12→Cys12"}},
	}
}

var testG12C = models.Mutation{Raw: "G12C", WildType: "G", Position: 12, Mutant: "C"}

func covalentHistory() []models.LigandDock {
	return []models.LigandDock{
		// Seed-dependent: a covalent call decided by the RNG.
		{SMILES: "SEED_DEPENDENT", WTScore: -9.3, MutantScore: -9.4, Selectivity: 0.13,
			Covalent: &models.CovalentDock{Feasibility: 0.09, ReachDistance: 3.94, AttackAngle: 131, Uncertain: true}},
		// The only molecule whose warhead reliably attacks the thiol.
		{SMILES: "FEASIBLE", WTScore: -8.7, MutantScore: -8.8, Selectivity: 0.10,
			Covalent: &models.CovalentDock{Feasibility: 0.71, ReachDistance: 3.66, AttackAngle: 108}},
		// Binds hardest of the three, but the warhead cannot attack — worthless.
		{SMILES: "INFEASIBLE", WTScore: -10.2, MutantScore: -9.5, Selectivity: -0.70,
			Covalent: &models.CovalentDock{Feasibility: 0, ReachDistance: 4.6, AttackAngle: 88}},
	}
}

// For a covalent target the docking selectivity is zero-mean noise: Gly12→Cys12 barely
// perturbs the reversible contact set, so WT and mutant scores agree to ~0.1 kcal/mol.
// A prompt that ranks history by selectivity hands the model that noise every round and
// asks it to chase it. Feasibility — can the warhead reach and attack the thiol — is the
// only measured covalent signal, so it must be what orders the history.
func TestCovalentPromptRanksHistoryByFeasibilityNotSelectivity(t *testing.T) {
	p := buildGenerationPrompt(testPocketContext(), testG12C, LookupKnownSite("P01116", testG12C, ""),
		"Cys12", covalentHistory(), 6)

	feasible := strings.Index(p, "FEASIBLE")
	infeasible := strings.Index(p, "INFEASIBLE")
	seedDep := strings.Index(p, "SEED_DEPENDENT")
	for name, idx := range map[string]int{"FEASIBLE": feasible, "INFEASIBLE": infeasible, "SEED_DEPENDENT": seedDep} {
		if idx < 0 {
			t.Fatalf("history molecule %s missing from the prompt", name)
		}
	}
	if feasible > infeasible {
		t.Error("the molecule whose warhead attacks the thiol must be listed above one that cannot")
	}
	// Ranking a seed-dependent call on its median would launder a coin flip into a
	// design lesson, so it sorts with the failures rather than on its 0.09.
	if seedDep < infeasible {
		t.Error("a seed-dependent covalent call must not outrank a measured negative")
	}
	// The WT score would invite the model to widen a gap that is sampling error.
	if strings.Contains(p, "-9.30") || strings.Contains(p, "wt -") {
		t.Error("the covalent prompt must not present WT docking scores as an optimisation target")
	}
	if strings.Contains(p, "higher selectivity is better") {
		t.Error("the covalent prompt still tells the model to maximise selectivity")
	}
}

// Left unsteered the generator returns what it remembers: truncated ARS-1620 analogues,
// below the 431–622 Da range of every switch-II inhibitor with cellular activity, and
// missing the aryl that fills the His95 groove.
func TestCovalentPromptCarriesTheCuratedDesignBrief(t *testing.T) {
	p := buildGenerationPrompt(testPocketContext(), testG12C, LookupKnownSite("P01116", testG12C, ""),
		"Cys12", nil, 6)

	for _, want := range []string{
		"COVALENT target",           // states the regime
		"Cys12",                     // names the anchor
		"warhead",                   // demands one
		"430–620 Da",                // the viable weight range
		"His95",                     // the potency-driving pharmacophore
		"ARS-1620",                  // prior art to avoid
		"sotorasib",                 // prior art to avoid
		"105°",                      // the attack trajectory, not just a distance
	} {
		if !strings.Contains(p, want) {
			t.Errorf("covalent prompt is missing %q", want)
		}
	}
}

// A mutation that installs no nucleophile has no covalent mechanism, and there the
// WT/mutant docking margin IS the signal. The old framing must survive for those runs.
func TestNonCovalentPromptKeepsTheSelectivityFraming(t *testing.T) {
	hist := []models.LigandDock{
		{SMILES: "WEAK", WTScore: -7.0, MutantScore: -7.2, Selectivity: 0.2},
		{SMILES: "STRONG", WTScore: -7.0, MutantScore: -9.5, Selectivity: 2.5},
	}
	gatekeeper := models.Mutation{Raw: "T790M", WildType: "T", Position: 790, Mutant: "M"}
	p := buildGenerationPrompt(testPocketContext(), gatekeeper, nil, "", hist, 4)

	if strings.Contains(p, "COVALENT target") {
		t.Error("a non-cysteine mutation was described as a covalent target")
	}
	if !strings.Contains(p, "higher selectivity is better") {
		t.Error("the non-covalent prompt lost its selectivity framing")
	}
	if strings.Index(p, "STRONG") > strings.Index(p, "WEAK") {
		t.Error("non-covalent history must still rank by selectivity")
	}
}

// The prompt's ranking key and the fitness function must agree about what an unreliable
// covalent call is worth: nothing.
func TestCovalentFeasibilityDiscountsUncertainAndAbsent(t *testing.T) {
	stable := models.LigandDock{Covalent: &models.CovalentDock{Feasibility: 0.4}}
	uncertain := models.LigandDock{Covalent: &models.CovalentDock{Feasibility: 0.9, Uncertain: true}}
	none := models.LigandDock{}

	if covalentFeasibility(stable) <= covalentFeasibility(uncertain) {
		t.Error("a stable 0.4 must rank above a seed-dependent 0.9")
	}
	if covalentFeasibility(none) > 0 {
		t.Error("a molecule with no warhead must not rank as feasible")
	}
}
