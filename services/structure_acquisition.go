package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/ayush00git/stanza/models"
)

// AcquireWTStructure runs the Stage-1 preference ladder for a run's wild-type
// structure: an experimental holo → apo structure (via FindBestExperimentalStructure)
// that resolves the mutated residue, falling back to the AlphaFold monomer model
// (via FetchComplexData) when none does. It then verifies the wild-type residue
// against the UniProt canonical sequence, annotating the result with notes.
func AcquireWTStructure(ctx context.Context, uniprotID string, mutation models.Mutation) (*models.WTStructure, error) {
	var chosen *models.WTStructure

	// 1. Prefer an experimental structure (holo before apo) that resolves the
	//    mutated residue. A lookup error is not fatal — we record it as a note and
	//    fall back to AlphaFold rather than failing the whole run.
	var lookupNote string
	exp, err := FindBestExperimentalStructure(ctx, uniprotID, mutation.Position, mutation.WildType)
	if err != nil {
		lookupNote = fmt.Sprintf("experimental structure lookup failed: %v", err)
	}

	if exp != nil {
		chosen = exp
	} else {
		// 2. Fall back to the AlphaFold monomer model.
		af, aerr := FetchComplexData(uniprotID)
		if aerr != nil {
			if lookupNote != "" {
				return nil, fmt.Errorf("%s; alphafold fallback failed: %w", lookupNote, aerr)
			}
			return nil, fmt.Errorf("alphafold fallback failed: %w", aerr)
		}
		notes := []string{"no experimental structure covering the residue; using AlphaFold model"}
		if lookupNote != "" {
			notes = append(notes, lookupNote)
		}
		chosen = &models.WTStructure{
			Source:       models.SourceAlphaFold,
			AlphafoldID:  af.MonomerEntryID,
			StructureURL: af.MonomerCifURL,
			Chain:        "A",
			LigandCount:  0,
			// AlphaFold models use UniProt (1-based) numbering on chain A, so the
			// mutated residue sits at its UniProt position with no remapping.
			TargetChain:     "A",
			TargetAuthSeqID: mutation.Position,
			Notes:           notes,
		}
	}

	// 3. Verify the wild-type residue against the UniProt canonical sequence.
	seq, serr := FetchUniProtSequence(ctx, uniprotID)
	if serr == nil && mutation.Position <= len(seq) {
		// Position is 1-based and guaranteed positive by ParseMutation.
		actual := string(seq[mutation.Position-1])
		chosen.WildTypeMatches = strings.EqualFold(actual, mutation.WildType)
		if chosen.Source == models.SourceAlphaFold {
			// AlphaFold models cover the full canonical sequence.
			chosen.ResidueResolved = true
		}
		if !chosen.WildTypeMatches {
			chosen.Notes = append(chosen.Notes, fmt.Sprintf(
				"warning: residue at UniProt position %d is %s, not the given wild-type %s (check numbering)",
				mutation.Position, strings.ToUpper(actual), mutation.WildType))
		}
	} else {
		// Could not verify: the sequence fetch failed or the position is out of
		// range. Leave WildTypeMatches=false and record why. Experimental
		// ResidueResolved is left as the PDB lookup set it.
		if serr != nil {
			chosen.Notes = append(chosen.Notes, fmt.Sprintf("could not verify wild-type residue: %v", serr))
		} else {
			chosen.Notes = append(chosen.Notes, fmt.Sprintf(
				"could not verify wild-type residue: position %d is beyond the %d-residue canonical sequence",
				mutation.Position, len(seq)))
		}
		if chosen.Source == models.SourceAlphaFold {
			chosen.ResidueResolved = mutation.Position <= len(seq)
		}
	}

	return chosen, nil
}
