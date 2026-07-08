package services

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ayush00git/stanza/models"
)

// aminoAcids is the set of standard one-letter amino-acid codes. Selenocysteine
// (U) and pyrrolysine (O) are intentionally excluded — Stage 1 only handles the
// 20 canonical residues UniProt uses for point substitutions.
const aminoAcids = "ACDEFGHIKLMNPQRSTVWY"

// mutationPattern matches a single point substitution: a letter, a run of digits,
// then a letter (e.g. "G12C"). Amino-acid validity and position sign are checked
// separately so each failure mode gets a distinct, human-readable error.
var mutationPattern = regexp.MustCompile(`^([A-Za-z])([0-9]+)([A-Za-z])$`)

// ParseMutation parses a point-substitution string like "G12C" (case-insensitive)
// into a models.Mutation. Returns an error with a clear message on any malformed input.
func ParseMutation(raw string) (models.Mutation, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return models.Mutation{}, fmt.Errorf("mutation is empty: provide a substitution like \"G12C\"")
	}

	m := mutationPattern.FindStringSubmatch(s)
	if m == nil {
		return models.Mutation{}, fmt.Errorf("mutation %q is malformed: expected <wild-type><position><mutant>, e.g. \"G12C\"", raw)
	}

	wildType, mutant := m[1], m[3]
	if !strings.Contains(aminoAcids, wildType) {
		return models.Mutation{}, fmt.Errorf("mutation %q has invalid wild-type amino acid %q: must be one of %s", raw, wildType, aminoAcids)
	}
	if !strings.Contains(aminoAcids, mutant) {
		return models.Mutation{}, fmt.Errorf("mutation %q has invalid mutant amino acid %q: must be one of %s", raw, mutant, aminoAcids)
	}

	// The regex guarantees only digits here, so any error is an overflow; either
	// way a non-positive value is meaningless for 1-based UniProt numbering.
	position, err := strconv.Atoi(m[2])
	if err != nil || position <= 0 {
		return models.Mutation{}, fmt.Errorf("mutation %q has invalid position %q: must be a positive integer", raw, m[2])
	}

	if wildType == mutant {
		return models.Mutation{}, fmt.Errorf("mutation %q is a no-op: wild-type and mutant residues are both %q", raw, wildType)
	}

	mut := models.Mutation{
		WildType: wildType,
		Position: position,
		Mutant:   mutant,
	}
	mut.Raw = mut.String() // normalized "G12C" form
	return mut, nil
}
