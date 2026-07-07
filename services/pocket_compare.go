package services

import (
	"fmt"
	"math"
	"sort"

	"github.com/ayush00git/stanza/models"
)

// DistanceThreshold is the generic distance in angstroms to consider two pockets as being the "same"
const DistanceThreshold = 6.0

// distance3D computes the Euclidean distance between two 3D points
func distance3D(a, b [3]float64) float64 {
	dx := a[0] - b[0]
	dy := a[1] - b[1]
	dz := a[2] - b[2]
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// ComparePockets generates the ComparisonResult requested by the frontend
func ComparePockets(monomerPockets, dimerPockets []models.Pocket, monomerPLDDT map[int]float64, targetChains map[string]bool) *models.ComparisonResult {
	result := &models.ComparisonResult{}

	monomerCount := len(monomerPockets)
	dimerCount := len(dimerPockets)
	result.SummaryMetrics.TotalMonomerPockets = monomerCount
	result.SummaryMetrics.TotalDimerPockets = dimerCount

	// 1. Compute basic averages & distributions
	var sumMonScore, sumDimScore float64
	var sumMonVol, sumDimVol float64
	var sumMonHydro, sumDimHydro float64
	var sumMonPolar, sumDimPolar float64

	for _, p := range monomerPockets {
		sumMonScore += p.Score
		sumMonVol += p.Volume
		sumMonHydro += p.Hydrophobicity
		sumMonPolar += p.Polarity
		result.GraphDatasets.DruggabilityDistMonomer = append(result.GraphDatasets.DruggabilityDistMonomer, p.Score)
	}

	for _, p := range dimerPockets {
		sumDimScore += p.Score
		sumDimVol += p.Volume
		sumDimHydro += p.Hydrophobicity
		sumDimPolar += p.Polarity
		result.GraphDatasets.DruggabilityDistDimer = append(result.GraphDatasets.DruggabilityDistDimer, p.Score)

		if p.IsInterfacePocket {
			result.SummaryMetrics.InterfacePocketCount++
		}

		result.GraphDatasets.StabilizationScatter = append(result.GraphDatasets.StabilizationScatter, models.ScatterData{
			PocketID:         p.PocketID,
			AvgDelta:         p.AvgDelta,
			DruggabilityScore: p.Score,
		})
	}

	if monomerCount > 0 {
		result.SummaryMetrics.AvgMonomerDruggability = sumMonScore / float64(monomerCount)
		result.PropertyChanges.MonomerAvgVolume = sumMonVol / float64(monomerCount)
		result.PropertyChanges.MonomerAvgHydrophobicity = sumMonHydro / float64(monomerCount)
		result.PropertyChanges.MonomerAvgPolarity = sumMonPolar / float64(monomerCount)
	}
	
	if dimerCount > 0 {
		result.SummaryMetrics.AvgDimerDruggability = sumDimScore / float64(dimerCount)
		result.PropertyChanges.DimerAvgVolume = sumDimVol / float64(dimerCount)
		result.PropertyChanges.DimerAvgHydrophobicity = sumDimHydro / float64(dimerCount)
		result.PropertyChanges.DimerAvgPolarity = sumDimPolar / float64(dimerCount)
	}

	result.DDGI = result.SummaryMetrics.AvgDimerDruggability - result.SummaryMetrics.AvgMonomerDruggability

	// 2. Map Pockets (Conserved, Disappeared, New)
	conserved := 0
	dimerMatched := make([]bool, dimerCount)

	for _, mp := range monomerPockets {
		matched := false
		for i, dp := range dimerPockets {
			if !dimerMatched[i] && distance3D(mp.Center, dp.Center) <= DistanceThreshold {
				matched = true
				dimerMatched[i] = true
				break
			}
		}
		if matched {
			conserved++
		}
	}

	result.PocketMapping.Conserved = conserved
	result.PocketMapping.MonomerOnly = monomerCount - conserved

	result.GraphDatasets.PocketCounts = []models.CountData{
		{Name: "Monomer", Count: monomerCount},
		{Name: "Dimer", Count: dimerCount},
		{Name: "Interface", Count: result.SummaryMetrics.InterfacePocketCount},
	}

	emergentCount := 0
	interfaceCount := 0

	for i, p := range dimerPockets {
		if dimerMatched[i] {
			dimerPockets[i].IsConserved = true
		} else {
			dimerPockets[i].IsEmergent = true
			// Pocket-level delta: pocket didn't exist in monomer → monomer=0 → delta = dimer pLDDT
			dimerPockets[i].AvgDelta = dimerPockets[i].AvgPLDDT
		}

		if p.IsInterfacePocket {
			result.InterfacePockets = append(result.InterfacePockets, dimerPockets[i])
			interfaceCount++
		}
		if dimerMatched[i] {
			result.ConservedPockets = append(result.ConservedPockets, dimerPockets[i])
		} else {
			result.EmergentPockets = append(result.EmergentPockets, dimerPockets[i])
			emergentCount++
		}
	}

	result.PocketMapping.Emergent = emergentCount
	result.PocketMapping.Interface = interfaceCount

	sort.Slice(result.InterfacePockets, func(i, j int) bool {
		if result.InterfacePockets[i].Score != result.InterfacePockets[j].Score {
			return result.InterfacePockets[i].Score > result.InterfacePockets[j].Score
		}
		return result.InterfacePockets[i].AvgDelta > result.InterfacePockets[j].AvgDelta
	})
	if result.InterfacePockets == nil {
		result.InterfacePockets = []models.Pocket{}
	}

	sort.Slice(result.ConservedPockets, func(i, j int) bool {
		if result.ConservedPockets[i].Score != result.ConservedPockets[j].Score {
			return result.ConservedPockets[i].Score > result.ConservedPockets[j].Score
		}
		return result.ConservedPockets[i].AvgDelta > result.ConservedPockets[j].AvgDelta
	})
	if result.ConservedPockets == nil {
		result.ConservedPockets = []models.Pocket{}
	}

	sort.Slice(result.EmergentPockets, func(i, j int) bool {
		if result.EmergentPockets[i].Score != result.EmergentPockets[j].Score {
			return result.EmergentPockets[i].Score > result.EmergentPockets[j].Score
		}
		return result.EmergentPockets[i].AvgDelta > result.EmergentPockets[j].AvgDelta
	})
	if result.EmergentPockets == nil {
		result.EmergentPockets = []models.Pocket{}
	}

	// 4. Fragment Comparison
	monomerFrags := make(map[string]bool)
	for _, p := range monomerPockets {
		for _, f := range p.Fragments {
			monomerFrags[f.ChemblID] = true
		}
	}

	dimerFragsMap := make(map[string]models.Fragment)
	for _, p := range dimerPockets {
		for _, f := range p.Fragments {
			if !monomerFrags[f.ChemblID] {
				dimerFragsMap[f.ChemblID] = f
			}
		}
	}
	
	interfaceFragsMap := make(map[string]models.Fragment)
	for _, p := range result.InterfacePockets {
		for _, f := range p.Fragments {
			if !monomerFrags[f.ChemblID] {
				interfaceFragsMap[f.ChemblID] = f
			}
		}
	}

	for _, f := range dimerFragsMap {
		result.FragmentComparison.UniqueDimerFragments = append(result.FragmentComparison.UniqueDimerFragments, f)
	}
	for _, f := range interfaceFragsMap {
		result.FragmentComparison.UniqueInterfaceFragments = append(result.FragmentComparison.UniqueInterfaceFragments, f)
	}
	// Sort for determinism
	sort.Slice(result.FragmentComparison.UniqueDimerFragments, func(i, j int) bool {
		return result.FragmentComparison.UniqueDimerFragments[i].Similarity > result.FragmentComparison.UniqueDimerFragments[j].Similarity
	})
	sort.Slice(result.FragmentComparison.UniqueInterfaceFragments, func(i, j int) bool {
		return result.FragmentComparison.UniqueInterfaceFragments[i].Similarity > result.FragmentComparison.UniqueInterfaceFragments[j].Similarity
	})
	if result.FragmentComparison.UniqueDimerFragments == nil {
		result.FragmentComparison.UniqueDimerFragments = []models.Fragment{}
	}
	if result.FragmentComparison.UniqueInterfaceFragments == nil {
		result.FragmentComparison.UniqueInterfaceFragments = []models.Fragment{}
	}

	// 5. Stabilization Stats
	positiveCount := 0
	interfaceOverlapSet := make(map[string]bool)
	globalOverlapSet := make(map[string]bool)

	for _, p := range result.InterfacePockets {
		for j, idx := range p.ResidueIndices {
			chain := ""
			if j < len(p.ResidueChains) {
				chain = p.ResidueChains[j]
			}
			resKey := fmt.Sprintf("%s:%d", chain, idx)
			interfaceOverlapSet[resKey] = true
		}
	}

	for _, p := range dimerPockets {
		for j, idx := range p.ResidueIndices {
			chain := ""
			if j < len(p.ResidueChains) {
				chain = p.ResidueChains[j]
			}
			resKey := fmt.Sprintf("%s:%d", chain, idx)
			if !globalOverlapSet[resKey] {
				globalOverlapSet[resKey] = true
				if targetChains[chain] {
					plddtKey := fmt.Sprintf("%s:%d", chain, idx)
					if monoScore, ok := monomerPLDDT[idx]; ok {
						delta := p.LocalPLDDT[plddtKey] - monoScore
						if delta > 0 && math.Abs(delta) <= 50.0 {
							positiveCount++
						}
					}
				}
				if interfaceOverlapSet[resKey] {
					result.StabilizationStats.ResiduesInInterfacePockets++
				}
			}
		}
	}
	result.StabilizationStats.ResiduesWithPositiveDelta = positiveCount

	if positiveCount > 0 {
		result.StabilizationStats.EnrichmentScore = float64(result.StabilizationStats.ResiduesInInterfacePockets) / float64(positiveCount)
	} else {
		result.StabilizationStats.EnrichmentScore = 0
	}

	return result
}
