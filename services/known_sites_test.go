package services

import (
	"strings"
	"testing"

	"github.com/ayush00git/stanza/models"
)

// The two pockets fpocket reports around Cys12 on the KRAS G12C AlphaFold model,
// with their measured druggability, centers and lining residues. Residue centroid
// of CYS A 12 is (8.61, 1.82, -2.65).
//
// pocket1 is the GDP/GTP nucleotide site; pocket9 is the switch-II pocket that
// sotorasib actually binds.
var (
	krasResidue12Center = [3]float64{8.61, 1.82, -2.65}

	krasNucleotidePocket = pocket(1, 0.632, [3]float64{4.67, 9.23, -3.98}, "A",
		12, 13, 15, 16, 17, 18, 21, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38,
		40, 58, 60, 61, 116, 117, 119, 120, 146, 147)

	krasSwitchIIPocket = pocket(9, 0.000, [3]float64{6.16, -3.49, 2.14}, "A",
		11, 12, 60, 61, 62, 65, 68, 92, 95, 96)

	krasG12C = models.Mutation{WildType: "G", Position: 12, Mutant: "C"}
)

func krasPockets() []models.Pocket {
	// fpocket order: sorted by druggability, so the nucleotide site leads.
	return []models.Pocket{krasNucleotidePocket, krasSwitchIIPocket}
}

// Documents the real limitation. The switch-II pocket is cryptic: on an apo model
// it is smaller, less druggable (0.00 vs 0.63) and no closer to Cys12 than the
// nucleotide site, so generic ranking legitimately prefers the nucleotide site.
// Only the curated registry recovers the right answer. If this test ever starts
// returning pocket 9, the geometric signal changed and the registry may be
// redundant for this target.
func TestProximityRankingAlonePicksNucleotideSiteForG12C(t *testing.T) {
	got := selectByProximity(krasPockets(), "A", 12, krasResidue12Center)
	if got == nil || got.PocketID != 1 {
		t.Fatalf("selectByProximity picked %v, want pocket 1 — generic ranking cannot see a cryptic site", got)
	}
}

// The fix: with the curated site, G12C targets the switch-II pocket.
func TestKnownSiteTargetsSwitchIIForKRASG12C(t *testing.T) {
	choice := selectResistancePocket(krasPockets(), "A", 12, "", "P01116", krasG12C, "")
	if choice.Pocket == nil {
		t.Fatal("selectResistancePocket returned no pocket")
	}
	if choice.Pocket.PocketID != 9 {
		t.Errorf("targeted pocket %d, want 9 (switch-II pocket)", choice.Pocket.PocketID)
	}
	if choice.Method != SelectionKnownSite {
		t.Errorf("method = %q, want %q", choice.Method, SelectionKnownSite)
	}
	if choice.SiteName != "switch-II pocket" {
		t.Errorf("site name = %q, want %q", choice.SiteName, "switch-II pocket")
	}
}

func TestMatchSitePocketSeparatesSwitchIIFromNucleotideSite(t *testing.T) {
	site := LookupKnownSite("P01116", krasG12C, "")
	if site == nil {
		t.Fatal("no curated site for KRAS G12C")
	}
	got, overlap := matchSitePocket(krasPockets(), site, "A")
	if got == nil || got.PocketID != 9 {
		t.Fatalf("matched pocket %v, want 9", got)
	}
	if overlap < minSiteOverlap {
		t.Errorf("overlap %.3f below the %.2f floor", overlap, minSiteOverlap)
	}
	// The nucleotide site shares only the P-loop residues, and is penalised for the
	// 23 residues it adds.
	nucOverlap := jaccard(pocketResidueSet(&krasNucleotidePocket, "A"), residueSet(site.Residues))
	if nucOverlap >= overlap {
		t.Errorf("nucleotide overlap %.3f must be below switch-II overlap %.3f", nucOverlap, overlap)
	}
	if nucOverlap >= minSiteOverlap {
		t.Errorf("nucleotide overlap %.3f must not clear the %.2f floor", nucOverlap, minSiteOverlap)
	}
}

func residueSet(rs []int) map[int]bool {
	m := make(map[int]bool, len(rs))
	for _, r := range rs {
		m[r] = true
	}
	return m
}

func TestLookupKnownSiteMatchesOnMutation(t *testing.T) {
	if got := LookupKnownSite("P01116", krasG12C, ""); got == nil {
		t.Fatal("G12C must match the curated switch-II site")
	}
	// A different KRAS mutation has no curated site: the S-IIP entry is specific to
	// the covalent G12C chemistry.
	g12d := models.Mutation{WildType: "G", Position: 12, Mutant: "D"}
	if got := LookupKnownSite("P01116", g12d, ""); got != nil {
		t.Errorf("G12D must not silently inherit the G12C site, got %q", got.Name)
	}
	// An unknown protein has no curated sites.
	if got := LookupKnownSite("P00533", krasG12C, ""); got != nil {
		t.Errorf("EGFR has no curated site, got %q", got.Name)
	}
}

func TestLookupKnownSiteHintOverridesMutation(t *testing.T) {
	g12d := models.Mutation{WildType: "G", Position: 12, Mutant: "D"}
	for _, hint := range []string{"switch-II", "Switch II", "switchII", "S-IIP", "siip", "sotorasib", "switch-II pocket"} {
		got := LookupKnownSite("P01116", g12d, hint)
		if got == nil {
			t.Errorf("hint %q must select the switch-II site", hint)
			continue
		}
		if got.Name != "switch-II pocket" {
			t.Errorf("hint %q selected %q", hint, got.Name)
		}
	}
}

// A hint naming no known site must not silently select an unrelated one.
func TestLookupKnownSiteUnrecognisedHintFallsBackToMutation(t *testing.T) {
	if got := LookupKnownSite("P01116", krasG12C, "allosteric-lobe"); got == nil || got.Name != "switch-II pocket" {
		t.Fatalf("unrecognised hint must fall through to the mutation match, got %v", got)
	}
	g12d := models.Mutation{WildType: "G", Position: 12, Mutant: "D"}
	if got := LookupKnownSite("P01116", g12d, "allosteric-lobe"); got != nil {
		t.Errorf("unrecognised hint with no mutation match must yield nil, got %q", got.Name)
	}
}

// A curated site whose pocket is absent from this structure (fully closed) must not
// fail the run — selection falls through to the generic ranking.
func TestKnownSiteFallsBackWhenPocketAbsent(t *testing.T) {
	// Only the nucleotide site was detected; the S-IIP never opened.
	only := []models.Pocket{krasNucleotidePocket}
	choice := selectResistancePocket(only, "A", 12, "", "P01116", krasG12C, "")
	if choice.Pocket == nil || choice.Pocket.PocketID != 1 {
		t.Fatalf("expected fallback to pocket 1, got %v", choice.Pocket)
	}
	if choice.Method != SelectionProximity {
		t.Errorf("method = %q, want %q on fallback", choice.Method, SelectionProximity)
	}
	if choice.SiteName != "" {
		t.Errorf("site name must be empty on fallback, got %q", choice.SiteName)
	}
}

func TestSelectResistancePocketUsesProximityForUncuratedTarget(t *testing.T) {
	egfr := models.Mutation{WildType: "T", Position: 790, Mutant: "M"}
	pockets := []models.Pocket{
		pocket(1, 0.7, [3]float64{25, 0, 0}, "A", 790),
		pocket(2, 0.1, [3]float64{1, 0, 0}, "A", 790),
	}
	choice := selectResistancePocket(pockets, "A", 790, "", "P00533", egfr, "")
	if choice.Method != SelectionProximity {
		t.Fatalf("method = %q, want %q", choice.Method, SelectionProximity)
	}
	if choice.Pocket == nil {
		t.Fatal("no pocket selected")
	}
}

func TestNormalizeSiteName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Switch-II", "switchii"},
		{"switch ii", "switchii"},
		{"  S-IIP  ", "siip"},
		{"switch_II/pocket", "switchiipocket"},
		{"", ""},
	} {
		if got := normalizeSiteName(tc.in); got != tc.want {
			t.Errorf("normalizeSiteName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestJaccard(t *testing.T) {
	a := residueSet([]int{1, 2, 3})
	b := residueSet([]int{2, 3, 4})
	if got := jaccard(a, b); got != 0.5 { // 2 shared / 4 union
		t.Errorf("jaccard = %v, want 0.5", got)
	}
	if got := jaccard(a, nil); got != 0 {
		t.Errorf("jaccard with empty set = %v, want 0", got)
	}
	if got := jaccard(a, a); got != 1 {
		t.Errorf("jaccard with itself = %v, want 1", got)
	}
}

// The switch-II pocket is cryptic: it barely exists on the apo AlphaFold model, so
// the curated site must also name the holo structure whose conformation contains it.
// Docking the same acrylamide into the AlphaFold pocket leaves its warhead 7.3 Å from
// the Cys12 thiol — beyond bonding range — where the 6OIM-derived receptor puts it at
// 3.8 Å. Without the template there is no covalent geometry to measure.
func TestSwitchIISiteCarriesHoloTemplate(t *testing.T) {
	site := LookupKnownSite("P01116", models.Mutation{WildType: "G", Position: 12, Mutant: "C"}, "")
	if site == nil {
		t.Fatal("no curated site for KRAS G12C")
	}
	if site.Template == nil {
		t.Fatal("switch-II site carries no structure template")
	}
	if site.Template.PDBID != "6OIM" {
		t.Errorf("template PDB = %q, want 6OIM", site.Template.PDBID)
	}
	if site.Template.Chain != "A" {
		t.Errorf("template chain = %q, want A", site.Template.Chain)
	}
	// 6OIM chain A numbers Met1 at author residue 1, so UniProt numbering carries
	// over unchanged and Cys12 sits at author 12.
	if site.Template.AuthOffset != 0 {
		t.Errorf("template AuthOffset = %d, want 0", site.Template.AuthOffset)
	}
}

// resolveBase must build KRAS G12C on the curated holo template rather than the apo
// model, reduce it to the one chain, and strip the bound sotorasib — which otherwise
// occupies the very pocket the run docks into.
func TestResolveBasePrefersCuratedTemplate(t *testing.T) {
	mut := models.Mutation{WildType: "G", Position: 12, Mutant: "C", Raw: "G12C"}
	base, err := resolveBase("P01116", mut, "")
	if err != nil {
		t.Fatalf("resolveBase: %v", err)
	}
	if !strings.Contains(base.url, "6OIM") {
		t.Errorf("base url = %q, want the 6OIM entry", base.url)
	}
	if base.chain != "A" {
		t.Errorf("base chain = %q, want A", base.chain)
	}
	if base.resnum != 12 {
		t.Errorf("base resnum = %d, want 12 (UniProt 12 + offset 0)", base.resnum)
	}
	if base.keepChain != "A" {
		t.Errorf("keepChain = %q, want A — a co-crystal carries chains we do not dock", base.keepChain)
	}
	if !base.stripHet {
		t.Error("stripHet = false; the bound inhibitor would occupy the switch-II pocket")
	}
}

// A mutation with no curated site must not borrow another site's template.
func TestResolveBaseIgnoresTemplateForOtherMutations(t *testing.T) {
	mut := models.Mutation{WildType: "G", Position: 13, Mutant: "D", Raw: "G13D"}
	if site := LookupKnownSite("P01116", mut, ""); site != nil {
		t.Fatalf("G13D unexpectedly matched curated site %q", site.Name)
	}
}
