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
