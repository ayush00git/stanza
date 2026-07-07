package services

import (
	"fmt"
	"sort"

	"github.com/ayush00git/stanza/models"
)

// InterfaceDeltaThreshold is the minimum average disorder delta (dimer pLDDT - monomer pLDDT)
// for a pocket to be classified as an interface pocket. A value of +5.0 means
// the dimer is at least 5 pLDDT points more ordered in that pocket region.
const InterfaceDeltaThreshold = 5.0

// MaxPockets is the maximum number of pockets returned after filtering.
const MaxPockets = 5

// FilterInterfacePockets computes the avg stabilization delta and sorts the pockets.
// targetChains identifies which chains in the complex correspond to the monomer protein.
func FilterInterfacePockets(pockets []models.Pocket, monomerPLDDT map[int]float64, targetChains map[string]bool, limit int) []models.Pocket {
	if len(pockets) == 0 {
		return pockets
	}

	for i := range pockets {
		var sumDelta float64
		var count int

		pockets[i].ResidueConfidences = make([]models.ResidueConfidence, 0, len(pockets[i].ResidueIndices))

		for j, idx := range pockets[i].ResidueIndices {
			chain := ""
			if j < len(pockets[i].ResidueChains) {
				chain = pockets[i].ResidueChains[j]
			}
			key := fmt.Sprintf("%s:%d", chain, idx)
			dimerScore := pockets[i].LocalPLDDT[key]

			monoScore := 0.0
			delta := 0.0

			if targetChains[chain] {
				if ms, ok := monomerPLDDT[idx]; ok {
					monoScore = ms
					delta = dimerScore - monoScore
					sumDelta += delta
					count++
				}
			}

			pockets[i].ResidueConfidences = append(pockets[i].ResidueConfidences, models.ResidueConfidence{
				ResidueIndex: idx,
				Chain:        chain,
				MonomerPLDDT: monoScore,
				DimerPLDDT:   dimerScore,
				Delta:        delta,
			})
		}

		if count > 0 {
			pockets[i].AvgDelta = sumDelta / float64(count)
		} else {
			pockets[i].AvgDelta = 0.0
		}
	}

	// Sort: interface pockets first, then by druggability score descending
	sort.SliceStable(pockets, func(i, j int) bool {
		if pockets[i].IsInterfacePocket != pockets[j].IsInterfacePocket {
			return pockets[i].IsInterfacePocket
		}
		return pockets[i].Score > pockets[j].Score
	})

	// Cap at limit
	if limit > 0 && len(pockets) > limit {
		pockets = pockets[:limit]
	}

	return pockets
}
