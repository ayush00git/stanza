package services

import (
	"strings"

	"github.com/ayush00git/stanza/models"
)

// Curated binding sites. Generic pocket ranking scores a cavity on how druggable
// and how close to the mutation it is. Both signals fail for a *cryptic* site: a
// pocket that only opens when a ligand is bound barely exists on an apo model, so
// fpocket reports it small and undruggable, and a bigger neighbouring cavity wins
// on every generic criterion.
//
// The switch-II pocket of KRAS G12C is the canonical example. On the AlphaFold
// model it is fpocket's 9th pocket — druggability 0.00, 463 A^3 — while the
// GDP/GTP nucleotide site scores 0.63 over 1071 A^3 and lists Cys12 among its
// lining residues too. Ranked generically the nucleotide site wins, and it is not
// wrong to: it really is more druggable, and Cys12 really does line more of it.
// It is simply not where the drugs bind.
//
// So a site the literature has already settled is named outright, and matched to
// a pocket by residue overlap rather than re-derived from geometry.

// SiteTemplate names the experimental structure whose conformation actually
// contains a curated site. A cryptic pocket is absent from the apo model that the
// pipeline otherwise builds on: the switch-II pocket only opens around a bound
// inhibitor, so an AlphaFold KRAS docks ligands ~1.6 kcal/mol weaker and leaves the
// warhead 7.3 A from the Cys12 thiol — beyond bonding range — where the same ligand
// docked into the sotorasib co-crystal reaches 4.5 A. Building the WT/mutant pair on
// the holo backbone is what makes the covalent geometry measurable at all.
//
// The bound ligand itself is stripped before docking; what we keep is the open
// side-chain conformation it induced.
type SiteTemplate struct {
	PDBID string // e.g. "6OIM"
	Chain string // author chain carrying the site
	// AuthOffset maps UniProt numbering onto this entry's author numbering:
	// auth_seq_id = uniprot_position + AuthOffset.
	AuthOffset int
	Reference  string // why this entry, and what is bound in it
}

// SiteGuidance is curated design knowledge for one site: what actually creates
// selectivity there, what a ligand must carry to be potent, and what has already been
// published. Like the residue set, it cannot be derived from geometry.
//
// Without it the generator reaches for what it remembers. Asked for KRAS G12C binders
// it returned truncated ARS-1620 analogues — one sharing 86% of its heavy atoms with
// the published compound — all of them 300–393 Da, below the 431–622 Da range of every
// switch-II inhibitor that has ever shown cellular activity, and all missing the aryl
// that fills the His95 groove.
type SiteGuidance struct {
	// Mechanism states, in one paragraph, where selectivity really comes from. For a
	// covalent target it is the bond, not the fit.
	Mechanism string
	// Pharmacophore names the substructure that drives potency in this pocket.
	Pharmacophore string
	// MinMW/MaxMW bound the molecular weight of ligands known to work here. Fragments
	// below the range reach the anchor residue but bind too weakly to matter.
	MinMW, MaxMW float64
	// PriorArt lists published inhibitors the model must not simply re-derive.
	PriorArt []string
	// MassAnchors are real ligands with their true masses, quoted to the model so it can
	// calibrate. Naming the window is not enough: told "430–620 Da", the generator returned
	// seven molecules of 386–421 Da in one round — every one just under the floor, every one
	// silently deleted. A model cannot weigh a SMILES string; it can copy a known mass.
	// Masses are PubChem values (see data/prior_art_kras_g12c.json), not recollections.
	MassAnchors []MassAnchor
}

// MassAnchor is one published ligand's molecular weight, used to calibrate the generator.
type MassAnchor struct {
	Name string
	MW   float64
}

// KnownSite is a curated binding site on one protein, identified by the residues
// that line it in UniProt canonical numbering.
type KnownSite struct {
	Name      string // human-readable, surfaced on the run
	UniprotID string
	// Residues lining the site, UniProt canonical numbering.
	Residues []int
	// Mutations this site is the design target for, e.g. "G12C". Empty means the
	// site applies to any mutation on this protein.
	Mutations []string
	// Aliases are normalized names a caller may pass as a site hint to select this
	// site explicitly, overriding the mutation match.
	Aliases []string
	// Reference is the structural evidence for the residue set.
	Reference string
	// Template is the structure to build the WT/mutant pair on. nil falls back to
	// the AlphaFold model.
	Template *SiteTemplate
	// Guidance conditions the molecule generator. nil leaves it unsteered.
	Guidance *SiteGuidance
}

// knownSites is the curated registry, keyed by UniProt accession.
var knownSites = map[string][]KnownSite{
	// KRAS.
	"P01116": {
		{
			Name:      "switch-II pocket",
			UniprotID: "P01116",
			// The S-IIP as lined in the sotorasib co-crystal: the P-loop around the
			// mutated Gly12, the switch-II helix (60-72), and the H95/Y96/Q99 wall
			// that only swings open on ligand binding.
			Residues:  []int{9, 10, 11, 12, 13, 58, 60, 61, 62, 63, 64, 65, 68, 69, 72, 95, 96, 99},
			Mutations: []string{"G12C"},
			Aliases:   []string{"switchii", "switch2", "siip", "sotorasib", "adagrasib"},
			Reference: "PDB 6OIM — sotorasib (AMG 510) bound to KRAS G12C",
			Template: &SiteTemplate{
				PDBID: "6OIM",
				Chain: "A",
				// 6OIM chain A carries an expression-tag GLY at author residue 0 and
				// then runs Met1..., so author numbering already equals UniProt
				// numbering and Cys12 sits at author 12.
				AuthOffset: 0,
				Reference:  "sotorasib covalently bound to Cys12; the S-IIP is open in this conformation",
			},
			Guidance: &SiteGuidance{
				Mechanism: "Selectivity here is covalent, not shape-based. A ligand slides into the switch-II " +
					"pocket of wild-type and G12C KRAS with essentially identical reversible affinity — pan-KRAS " +
					"binders engage WT, G12C, G12D, G12V and G13D at Kd 10-40 nM, and adagrasib itself binds " +
					"wild-type KRAS tightly and non-covalently. What the mutant alone provides is the Cys12 thiol. " +
					"The warhead's electrophilic carbon must reach that sulfur and attack it along a viable " +
					"trajectory; wild-type Gly12 offers nothing to bond, so the drug simply washes back out. " +
					"Reversible affinity for this pocket is famously weak (ARS-853 Ki about 200 uM), so potency " +
					"is carried by the covalent step, not by binding tighter.",
				Pharmacophore: "an aryl or heteroaryl substituent reaching the cryptic His95/Tyr96/Gln99 groove. " +
					"This is the single largest potency driver in the series: adding it took ARS-853 (2.5 uM) to " +
					"ARS-1620. A molecule that only occupies the front of the pocket near Cys12 will reach the " +
					"thiol and still be far too weak.",
				MinMW: 430,
				MaxMW: 620,
				// The two tool compounds sit ON the floor and the two drugs mid-window: together
				// they say "the floor is real, and the useful mass is higher than you think".
				MassAnchors: []MassAnchor{
					{"ARS-1620", 430.8}, {"ARS-853", 433.0},
					{"sotorasib", 560.6}, {"adagrasib", 604.1},
				},
				PriorArt: []string{
					"sotorasib (AMG 510)", "adagrasib (MRTX849)", "divarasib (GDC-6036)",
					"ARS-1620", "ARS-853",
					"4-(piperazin-1-yl)quinazoline bearing an N-acyl warhead (the ARS-1620 core)",
				},
			},
		},
	},
}

// minSiteOverlap is the Jaccard floor for accepting a pocket as a curated site.
// The S-IIP pocket overlaps its curated residue set at ~0.47; the nucleotide site,
// which shares only the P-loop residues, reaches ~0.12.
const minSiteOverlap = 0.20

// SelectionKnownSite marks a resistance pocket chosen from the curated registry.
const SelectionKnownSite = "known_site"

// LookupKnownSite returns the curated site to design against for this protein, or
// nil. An explicit hint selects a site by alias or name and overrides the mutation
// match; otherwise the site is the one curated for this mutation.
func LookupKnownSite(uniprotID string, mut models.Mutation, hint string) *KnownSite {
	sites := knownSites[strings.ToUpper(strings.TrimSpace(uniprotID))]
	if len(sites) == 0 {
		return nil
	}

	if h := normalizeSiteName(hint); h != "" {
		for i := range sites {
			if siteMatchesName(&sites[i], h) {
				return &sites[i]
			}
		}
		// A hint that names no known site falls through to the mutation match rather
		// than silently selecting an unrelated site.
	}

	raw := mut.String()
	for i := range sites {
		if len(sites[i].Mutations) == 0 || containsFold(sites[i].Mutations, raw) {
			return &sites[i]
		}
	}
	return nil
}

// siteMatchesName reports whether a normalized hint names this site.
func siteMatchesName(s *KnownSite, hint string) bool {
	if normalizeSiteName(s.Name) == hint {
		return true
	}
	for _, a := range s.Aliases {
		if normalizeSiteName(a) == hint {
			return true
		}
	}
	return false
}

// normalizeSiteName lowercases and strips separators so "Switch-II", "switch ii"
// and "switchII" all compare equal.
func normalizeSiteName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case ' ', '-', '_', '.', '/':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func containsFold(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}

// matchSitePocket returns the pocket whose lining residues best overlap the curated
// site, by Jaccard index, provided the overlap clears minSiteOverlap. Jaccard —
// rather than plain coverage — is what discriminates here: a large pocket that
// happens to contain a few of the site's residues is penalised for the many
// residues it adds, which is precisely how the nucleotide site loses to the S-IIP.
func matchSitePocket(pockets []models.Pocket, site *KnownSite, chain string) (*models.Pocket, float64) {
	want := make(map[int]bool, len(site.Residues))
	for _, r := range site.Residues {
		want[r] = true
	}

	var best *models.Pocket
	bestJ := 0.0
	for i := range pockets {
		j := jaccard(pocketResidueSet(&pockets[i], chain), want)
		// Ties break on the lower pocket ID for deterministic selection.
		if j > bestJ || (best != nil && j == bestJ && pockets[i].PocketID < best.PocketID) {
			bestJ = j
			best = &pockets[i]
		}
	}
	if best == nil || bestJ < minSiteOverlap {
		return nil, bestJ
	}
	return best, bestJ
}

// pocketResidueSet is a pocket's lining residue indices on the target chain.
func pocketResidueSet(p *models.Pocket, chain string) map[int]bool {
	set := make(map[int]bool, len(p.ResidueIndices))
	for k, idx := range p.ResidueIndices {
		if k < len(p.ResidueChains) && p.ResidueChains[k] != chain {
			continue
		}
		set[idx] = true
	}
	return set
}

// jaccard is |a∩b| / |a∪b|, zero when both sets are empty.
func jaccard(a, b map[int]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
