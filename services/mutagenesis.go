package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ayush00git/stanza/models"
)

const (
	// mutateScript is the PDBFixer helper, resolved relative to the server's
	// working directory (the repo root, like the fpocket ./tmp scratch dir).
	mutateScript = "scripts/mutate.py"
	// runsDataDir holds each run's generated structures. It lives under the
	// gitignored ./tmp tree and is served back via GET /runs/:id/structure/:track.
	runsDataDir = "tmp/runs"
	// mutagenesisTool names the engine recorded on the result.
	mutagenesisTool = "pdbfixer"
)

// aaThreeLetter maps one-letter amino-acid codes to their 3-letter residue names,
// which is what the mutagenesis engine expects.
var aaThreeLetter = map[string]string{
	"A": "ALA", "R": "ARG", "N": "ASN", "D": "ASP", "C": "CYS",
	"E": "GLU", "Q": "GLN", "G": "GLY", "H": "HIS", "I": "ILE",
	"L": "LEU", "K": "LYS", "M": "MET", "F": "PHE", "P": "PRO",
	"S": "SER", "T": "THR", "W": "TRP", "Y": "TYR", "V": "VAL",
}

// RunStructureDir returns the directory holding a run's generated structures.
func RunStructureDir(runID string) string { return filepath.Join(runsDataDir, runID) }

// RunStructurePath returns the path to a run's generated structure for a track
// ("wt" or "mutant").
func RunStructurePath(runID, track string) string {
	return filepath.Join(RunStructureDir(runID), track+".pdb")
}

// BuildMutagenesis is Stage 2. From the acquired base structure it builds a matched
// wild-type/mutant pair by side-chain mutagenesis: the base residue at the target
// position is set to the wild-type residue for the WT track and to the mutant residue
// for the mutant track. Both are written in the same coordinate frame (a clean
// comparison basis, robust to the base crystal already carrying the mutation), under
// the run's structure directory, and served via GET /runs/:id/structure/:track.
func BuildMutagenesis(ctx context.Context, runID, uniprotID string, mutation models.Mutation) (*models.MutagenesisResult, error) {
	wtRes, ok1 := aaThreeLetter[strings.ToUpper(mutation.WildType)]
	mutRes, ok2 := aaThreeLetter[strings.ToUpper(mutation.Mutant)]
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("mutagenesis: unknown amino-acid code in %q", mutation.Raw)
	}

	// Build the matched pair on the AlphaFold model. Its residue numbering is
	// exactly the UniProt sequence (chain A, 1-based), which removes the
	// residue-mapping ambiguity of experimental structures and guarantees we
	// mutate the intended residue. (Stage 1's experimental pick is still reported
	// on the run for reference; mutating it directly needs rigorous SIFTS
	// residue-level mapping — a follow-up.)
	af, err := FetchComplexData(uniprotID)
	if err != nil {
		return nil, fmt.Errorf("mutagenesis: fetch AlphaFold base: %w", err)
	}
	if af.MonomerCifURL == "" {
		return nil, fmt.Errorf("mutagenesis: no AlphaFold model available for %s", uniprotID)
	}
	chain := "A"
	resnum := mutation.Position

	dir := RunStructureDir(runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mutagenesis: create run dir: %w", err)
	}

	basePath := filepath.Join(dir, "base.cif")
	if err := downloadFile(ctx, af.MonomerCifURL, basePath); err != nil {
		return nil, fmt.Errorf("mutagenesis: download base structure: %w", err)
	}

	// Build both tracks from the same base so they share a backbone frame.
	if err := runMutate(ctx, basePath, chain, resnum, wtRes, RunStructurePath(runID, "wt")); err != nil {
		return nil, fmt.Errorf("mutagenesis: build WT structure: %w", err)
	}
	if err := runMutate(ctx, basePath, chain, resnum, mutRes, RunStructurePath(runID, "mutant")); err != nil {
		return nil, fmt.Errorf("mutagenesis: build mutant structure: %w", err)
	}

	return &models.MutagenesisResult{
		Tool:               mutagenesisTool,
		WTStructureURL:     fmt.Sprintf("/runs/%s/structure/wt", runID),
		MutantStructureURL: fmt.Sprintf("/runs/%s/structure/mutant", runID),
		TargetChain:        chain,
		TargetResidueNum:   resnum,
		WildTypeResidue:    wtRes,
		MutantResidue:      mutRes,
		Notes: []string{
			fmt.Sprintf("built from the AlphaFold model %s (UniProt numbering, chain A)", af.MonomerEntryID),
			"WT and mutant share one backbone frame — they differ only at the target residue",
		},
	}, nil
}

// runMutate shells out to the PDBFixer helper for one single-residue mutation.
func runMutate(ctx context.Context, input, chain string, resnum int, to, out string) error {
	cmd := exec.CommandContext(ctx, "python3", mutateScript,
		"--input", input,
		"--chain", chain,
		"--resnum", fmt.Sprint(resnum),
		"--to", to,
		"--out", out,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
