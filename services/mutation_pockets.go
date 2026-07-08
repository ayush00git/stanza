package services

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ayush00git/stanza/models"
)

const (
	// maxRunPockets caps how many pockets per track are returned in the response.
	maxRunPockets = 12
	// maxKeyResidues caps the resistance pocket's key-residue list for the prompt.
	maxKeyResidues = 12
	// resistancePairRadius (Å) is how far the corresponding WT pocket may sit from
	// the mutant resistance pocket's center to still count as the same site.
	resistancePairRadius = 8.0
)

// BuildPocketAnalysis is Stage 3. It runs fpocket on the run's matched WT and
// mutant structures, matches pockets across the two tracks, identifies the
// resistance pocket (the one containing the mutated residue), and computes the
// WT→mutant pocket delta the generation loop conditions on.
func BuildPocketAnalysis(ctx context.Context, run *models.Run) (*models.PocketAnalysis, error) {
	if run.Mutagenesis == nil {
		return nil, fmt.Errorf("pocket analysis: run has no mutant structure (run Stage-2 mutagenesis first)")
	}

	wtPath := RunStructurePath(run.ID, "wt")
	mutPath := RunStructurePath(run.ID, "mutant")

	wtPockets, err := RunFpocketFile(wtPath)
	if err != nil {
		return nil, fmt.Errorf("pocket analysis: fpocket on WT structure: %w", err)
	}
	mutPockets, err := RunFpocketFile(mutPath)
	if err != nil {
		return nil, fmt.Errorf("pocket analysis: fpocket on mutant structure: %w", err)
	}
	for i := range wtPockets {
		wtPockets[i].SourceType = "wt"
	}
	for i := range mutPockets {
		mutPockets[i].SourceType = "mutant"
	}

	chain := run.Mutagenesis.TargetChain
	pos := run.Mutagenesis.TargetResidueNum

	conserved, wtOnly, emergent := matchPockets(wtPockets, mutPockets)

	// The resistance pocket on the mutant track: the one containing the mutated
	// residue — the site we design against. For a clean delta, pair it with its
	// spatially-corresponding WT pocket (nearest center) rather than the WT pocket
	// independently found around the residue: the two structures differ by a single
	// side chain, so the same pocket barely moves, and this avoids inflated deltas
	// when fpocket partitions the residue's pocket differently between the tracks.
	mutRP := findResistancePocket(mutPockets, chain, pos, mutPath)
	var wtRP *models.Pocket
	if mutRP != nil {
		wtRP = nearestPocketWithin(wtPockets, mutRP.Center, resistancePairRadius)
	}

	var context *models.MutantPocketContext
	if mutRP != nil {
		context = buildMutantContext(wtRP, mutRP, chain, pos,
			run.Mutagenesis.WildTypeResidue, run.Mutagenesis.MutantResidue)
	}

	return &models.PocketAnalysis{
		WTPockets:      capPockets(wtPockets, maxRunPockets),
		MutantPockets:  capPockets(mutPockets, maxRunPockets),
		ConservedCount: conserved,
		WTOnlyCount:    wtOnly,
		EmergentCount:  emergent,
		Context:        context,
	}, nil
}

// matchPockets greedily pairs WT and mutant pockets by 3D center distance (reusing
// distance3D + DistanceThreshold). A matched WT pocket is conserved; an unmatched WT
// pocket is WT-only (the mutation closed it); an unmatched mutant pocket is emergent
// (the mutation opened it).
func matchPockets(wt, mutant []models.Pocket) (conserved, wtOnly, emergent int) {
	matched := make([]bool, len(mutant))
	for _, w := range wt {
		found := false
		for j := range mutant {
			if !matched[j] && distance3D(w.Center, mutant[j].Center) <= DistanceThreshold {
				matched[j] = true
				found = true
				break
			}
		}
		if found {
			conserved++
		} else {
			wtOnly++
		}
	}
	for _, m := range matched {
		if !m {
			emergent++
		}
	}
	return conserved, wtOnly, emergent
}

// findResistancePocket returns the pocket that contains the mutated residue on the
// target chain. Falls back to the pocket whose center is nearest the mutated
// residue's atoms when no pocket lists the residue (allosteric / surface mutation).
func findResistancePocket(pockets []models.Pocket, chain string, pos int, structPath string) *models.Pocket {
	for i := range pockets {
		p := &pockets[i]
		for k, idx := range p.ResidueIndices {
			if idx != pos {
				continue
			}
			if k < len(p.ResidueChains) && p.ResidueChains[k] != chain {
				continue
			}
			return p
		}
	}

	// Fallback: nearest pocket center to the mutated residue's coordinates.
	coords := getResiduesCoordsFromOriginal(structPath, []int{pos}, []string{chain})
	if len(coords) == 0 || len(pockets) == 0 {
		return nil
	}
	target := computeCenter(coords)
	best := &pockets[0]
	bestD := distance3D(best.Center, target)
	for i := 1; i < len(pockets); i++ {
		if d := distance3D(pockets[i].Center, target); d < bestD {
			bestD = d
			best = &pockets[i]
		}
	}
	return best
}

// nearestPocketWithin returns the pocket whose center is closest to the given
// point, provided it is within maxDist; otherwise nil.
func nearestPocketWithin(pockets []models.Pocket, center [3]float64, maxDist float64) *models.Pocket {
	var best *models.Pocket
	bestD := math.MaxFloat64
	for i := range pockets {
		if d := distance3D(pockets[i].Center, center); d < bestD {
			bestD = d
			best = &pockets[i]
		}
	}
	if best != nil && bestD <= maxDist {
		return best
	}
	return nil
}

// buildMutantContext assembles the resistance pocket + WT→mutant delta payload.
func buildMutantContext(wtRP, mutRP *models.Pocket, chain string, pos int, wtName, mutName string) *models.MutantPocketContext {
	mp := models.MutantPocket{
		KeyResidues:    keyResidues(mutRP, chain, pos, mutName),
		Volume:         mutRP.Volume,
		Hydrophobicity: mutRP.Hydrophobicity,
		Polarity:       mutRP.Polarity,
		Center:         mutRP.Center,
		PocketID:       mutRP.PocketID,
	}

	delta := models.PocketDelta{
		Changed: []string{fmt.Sprintf("%s→%s", resToken(wtName, pos), resToken(mutName, pos))},
	}
	if wtRP != nil {
		delta.DVolume = round1(mutRP.Volume - wtRP.Volume)
		delta.DHydrophobicity = round1(mutRP.Hydrophobicity - wtRP.Hydrophobicity)
		delta.DPolarity = round1(mutRP.Polarity - wtRP.Polarity)
		delta.ResiduesGained, delta.ResiduesLost = residueSetDiff(wtRP, mutRP, chain, pos)
	}
	delta.Effect = effectSummary(delta)

	return &models.MutantPocketContext{MutantPocket: mp, PocketDelta: delta}
}

// keyResidues returns the mutated residue first, then the pocket's other lining
// residues ranked by sequence proximity to the mutation, capped at maxKeyResidues.
func keyResidues(p *models.Pocket, chain string, pos int, mutName string) []string {
	type rr struct {
		token string
		idx   int
	}
	mutTok := resToken(mutName, pos)
	seen := map[string]bool{mutTok: true}
	var others []rr
	for k, idx := range p.ResidueIndices {
		if k < len(p.ResidueChains) && p.ResidueChains[k] != chain {
			continue
		}
		if idx == pos {
			continue
		}
		name := "UNK"
		if k < len(p.ResidueNames) {
			name = p.ResidueNames[k]
		}
		tok := resToken(name, idx)
		if seen[tok] {
			continue
		}
		seen[tok] = true
		others = append(others, rr{tok, idx})
	}
	sort.Slice(others, func(i, j int) bool {
		di, dj := absInt(others[i].idx-pos), absInt(others[j].idx-pos)
		if di != dj {
			return di < dj
		}
		return others[i].idx < others[j].idx
	})

	out := []string{mutTok}
	for _, o := range others {
		if len(out) >= maxKeyResidues {
			break
		}
		out = append(out, o.token)
	}
	return out
}

// residueSetDiff diffs the two pockets' lining residues (by position on the target
// chain): residues gained in the mutant pocket and lost from the WT pocket. The
// mutated position itself is excluded — it is reported separately in `changed`.
func residueSetDiff(wtRP, mutRP *models.Pocket, chain string, pos int) (gained, lost []string) {
	wtSet := pocketResidueTokens(wtRP, chain)
	mutSet := pocketResidueTokens(mutRP, chain)
	for idx, tok := range mutSet {
		if idx == pos {
			continue // the mutated residue is reported in `changed`, not gained
		}
		if _, ok := wtSet[idx]; !ok {
			gained = append(gained, tok)
		}
	}
	for idx, tok := range wtSet {
		if idx == pos {
			continue
		}
		if _, ok := mutSet[idx]; !ok {
			lost = append(lost, tok)
		}
	}
	sort.Strings(gained)
	sort.Strings(lost)
	return gained, lost
}

// pocketResidueTokens maps each target-chain residue index in the pocket to its
// RESNAME+INDEX token.
func pocketResidueTokens(p *models.Pocket, chain string) map[int]string {
	m := make(map[int]string, len(p.ResidueIndices))
	for k, idx := range p.ResidueIndices {
		if k < len(p.ResidueChains) && p.ResidueChains[k] != chain {
			continue
		}
		name := "UNK"
		if k < len(p.ResidueNames) {
			name = p.ResidueNames[k]
		}
		m[idx] = resToken(name, idx)
	}
	return m
}

// effectSummary renders a one-line, model-readable summary of the pocket delta.
func effectSummary(d models.PocketDelta) string {
	var b strings.Builder
	if len(d.Changed) > 0 {
		b.WriteString(d.Changed[0])
	}
	switch {
	case d.DVolume < -5:
		b.WriteString(": tighter pocket")
	case d.DVolume > 5:
		b.WriteString(": larger pocket")
	default:
		b.WriteString(": similar pocket size")
	}
	if d.DHydrophobicity > 2 {
		b.WriteString(", more hydrophobic")
	} else if d.DHydrophobicity < -2 {
		b.WriteString(", less hydrophobic")
	}
	if d.DPolarity < -2 {
		b.WriteString(", reduced polarity")
	} else if d.DPolarity > 2 {
		b.WriteString(", increased polarity")
	}
	b.WriteString(".")
	return b.String()
}

// resToken renders a residue as a stable RESNAME+INDEX token (e.g. "Met790").
func resToken(name3 string, idx int) string {
	return title3(name3) + strconv.Itoa(idx)
}

// title3 title-cases a 3-letter residue name: "THR" -> "Thr".
func title3(name string) string {
	if name == "" {
		return name
	}
	n := strings.ToUpper(name)
	return n[:1] + strings.ToLower(n[1:])
}

// capPockets returns at most n pockets (they arrive sorted by druggability).
func capPockets(p []models.Pocket, n int) []models.Pocket {
	if len(p) > n {
		return p[:n]
	}
	return p
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// round1 rounds to one decimal place to keep the delta payload tidy.
func round1(x float64) float64 { return math.Round(x*10) / 10 }

// round2 rounds to two decimal places (used for docking scores/selectivity).
func round2(x float64) float64 { return math.Round(x*100) / 100 }
