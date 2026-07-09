package services

import (
	"math"
	"sort"

	"github.com/ayush00git/stanza/models"
)

// Resistance-pocket selection. Stage 3 must decide which of fpocket's pockets is
// the site the pipeline designs against. Picking the first pocket that merely
// lists the mutated residue is wrong: fpocket returns pockets sorted by
// druggability, and a large, highly druggable pocket often carries the mutated
// residue on its rim while the pocket that actually surrounds the residue ranks
// far lower. Docking then centres on a cavity many angstroms from the mutation,
// the mutated side chain falls outside Vina's 8 Å pairwise cutoff, and the WT and
// mutant tracks score identically — selectivity collapses to pose noise.
//
// Instead, score every candidate by a druggability-weighted proximity to the
// mutated residue and take the best.

const (
	// proximityLengthScale (Å) sets how quickly the proximity term decays with the
	// distance from the mutated residue to a pocket's center. At one length scale
	// the term has fallen to 1/e.
	proximityLengthScale = 8.0

	// siteSearchRadius (Å) bounds which pockets are considered at all when no
	// pocket lists the mutated residue (an allosteric or surface mutation).
	siteSearchRadius = 15.0

	// druggabilityFloor keeps a pocket with a druggability of 0 from being
	// annihilated by the multiplicative weighting. Cryptic pockets — which is
	// exactly what a resistance site often is — routinely score 0.00 on an apo
	// structure, so a bare product would rank them last on principle.
	druggabilityFloor = 0.25
)

// Pocket-selection methods, reported on the resistance pocket so a caller can see
// why this site was chosen.
const (
	SelectionProximity = "druggability_weighted_proximity"
)

// scoredPocket pairs a candidate pocket with its selection score.
type scoredPocket struct {
	pocket *models.Pocket
	score  float64
}

// rankResistanceCandidates scores and orders candidate pockets by
// druggability-weighted proximity to resCenter, best first. Ties break on the
// lower pocket ID so selection is deterministic.
func rankResistanceCandidates(cands []*models.Pocket, resCenter [3]float64) []scoredPocket {
	out := make([]scoredPocket, len(cands))
	for i, p := range cands {
		out[i] = scoredPocket{pocket: p, score: siteScore(*p, resCenter)}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].score != out[b].score {
			return out[a].score > out[b].score
		}
		return out[a].pocket.PocketID < out[b].pocket.PocketID
	})
	return out
}

// siteScore is the druggability-weighted proximity of one pocket to the mutated
// residue: a proximity term that decays exponentially with distance, scaled by a
// floored druggability weight.
func siteScore(p models.Pocket, resCenter [3]float64) float64 {
	return proximityTerm(distance3D(p.Center, resCenter)) * druggabilityWeight(p.Score)
}

// proximityTerm maps a distance (Å) to (0,1], decaying with proximityLengthScale.
func proximityTerm(d float64) float64 {
	if d < 0 {
		d = 0
	}
	return math.Exp(-d / proximityLengthScale)
}

// druggabilityWeight maps an fpocket druggability score to [druggabilityFloor, 1].
func druggabilityWeight(drug float64) float64 {
	return druggabilityFloor + (1-druggabilityFloor)*clamp01(drug)
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// pocketsContaining returns the pockets whose lining residues include position pos
// on the target chain.
func pocketsContaining(pockets []models.Pocket, chain string, pos int) []*models.Pocket {
	var out []*models.Pocket
	for i := range pockets {
		if pocketHasResidue(&pockets[i], chain, pos) {
			out = append(out, &pockets[i])
		}
	}
	return out
}

// pocketHasResidue reports whether a pocket lists (chain, pos) among its lining
// residues.
func pocketHasResidue(p *models.Pocket, chain string, pos int) bool {
	for k, idx := range p.ResidueIndices {
		if idx != pos {
			continue
		}
		if k < len(p.ResidueChains) && p.ResidueChains[k] != chain {
			continue
		}
		return true
	}
	return false
}

// pocketsWithin returns the pockets whose center lies within maxDist of center.
func pocketsWithin(pockets []models.Pocket, center [3]float64, maxDist float64) []*models.Pocket {
	var out []*models.Pocket
	for i := range pockets {
		if distance3D(pockets[i].Center, center) <= maxDist {
			out = append(out, &pockets[i])
		}
	}
	return out
}

// allPockets returns pointers to every pocket, used as the last-resort candidate
// set so selection never returns nil for a non-empty pocket list.
func allPockets(pockets []models.Pocket) []*models.Pocket {
	out := make([]*models.Pocket, len(pockets))
	for i := range pockets {
		out[i] = &pockets[i]
	}
	return out
}

// selectByProximity picks the resistance pocket by druggability-weighted proximity.
// Pockets that actually line the mutated residue are preferred as a group; only
// when none do (an allosteric or surface mutation) does it widen to pockets near
// the residue, and finally to every pocket.
func selectByProximity(pockets []models.Pocket, chain string, pos int, resCenter [3]float64) *models.Pocket {
	if len(pockets) == 0 {
		return nil
	}
	cands := pocketsContaining(pockets, chain, pos)
	if len(cands) == 0 {
		cands = pocketsWithin(pockets, resCenter, siteSearchRadius)
	}
	if len(cands) == 0 {
		cands = allPockets(pockets)
	}
	ranked := rankResistanceCandidates(cands, resCenter)
	return ranked[0].pocket
}

// residueCenter returns the centroid of a residue's atoms in the given structure,
// and false when the residue is absent (or the file cannot be read).
func residueCenter(structPath, chain string, pos int) ([3]float64, bool) {
	if structPath == "" {
		return [3]float64{}, false
	}
	coords := getResiduesCoordsFromOriginal(structPath, []int{pos}, []string{chain})
	if len(coords) == 0 {
		return [3]float64{}, false
	}
	return computeCenter(coords), true
}

// selectResistancePocket returns the pocket the pipeline designs against, together
// with the method that chose it.
func selectResistancePocket(pockets []models.Pocket, chain string, pos int, structPath string) (*models.Pocket, string) {
	if len(pockets) == 0 {
		return nil, ""
	}
	resCenter, ok := residueCenter(structPath, chain, pos)
	if !ok {
		// Without the residue's coordinates the proximity term is meaningless; fall
		// back to the most druggable pocket that lines the residue.
		cands := pocketsContaining(pockets, chain, pos)
		if len(cands) == 0 {
			return nil, ""
		}
		best := cands[0]
		for _, p := range cands[1:] {
			if p.Score > best.Score {
				best = p
			}
		}
		return best, SelectionProximity
	}
	return selectByProximity(pockets, chain, pos, resCenter), SelectionProximity
}
