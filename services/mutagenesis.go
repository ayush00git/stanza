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

// mutateBase is the structure the WT/mutant pair is built on.
type mutateBase struct {
	url    string
	chain  string
	resnum int    // author numbering of the target residue in this structure
	label  string // provenance, recorded on the result
	// keepChain/stripHet reduce a co-crystal to the docking unit: one chain, no
	// bound inhibitor sitting in the pocket we are about to dock into.
	keepChain string
	stripHet  bool
}

// resolveBase picks the structure to build on. A curated site template wins: a
// cryptic pocket like the KRAS switch-II site does not exist on the apo AlphaFold
// model, and docking into a pocket that is not open cannot measure a warhead's reach
// to the thiol. Everything else falls back to the AlphaFold monomer, whose residue
// numbering is exactly the UniProt sequence.
//
// Stage 1's generic experimental pick is deliberately NOT used here: it ranks by
// resolution and "has a drug-like ligand" without checking which residue actually
// sits at the mutated position, and for KRAS G12C it returns 7ROV — a G12D structure
// whose "ligand" is a GTP analogue in the nucleotide site.
func resolveBase(uniprotID string, mutation models.Mutation, siteHint string) (mutateBase, error) {
	if site := LookupKnownSite(uniprotID, mutation, siteHint); site != nil && site.Template != nil {
		t := site.Template
		return mutateBase{
			url:       fmt.Sprintf("%s/%s.cif", rcsbDownloadBase, t.PDBID),
			chain:     t.Chain,
			resnum:    mutation.Position + t.AuthOffset,
			keepChain: t.Chain,
			stripHet:  true,
			label: fmt.Sprintf("built from PDB %s chain %s — the %s conformation (%s)",
				t.PDBID, t.Chain, site.Name, t.Reference),
		}, nil
	}

	af, err := FetchComplexData(uniprotID)
	if err != nil {
		return mutateBase{}, fmt.Errorf("fetch AlphaFold base: %w", err)
	}
	if af.MonomerCifURL == "" {
		return mutateBase{}, fmt.Errorf("no AlphaFold model available for %s", uniprotID)
	}
	return mutateBase{
		url:    af.MonomerCifURL,
		chain:  "A",
		resnum: mutation.Position,
		label:  fmt.Sprintf("built from the AlphaFold model %s (UniProt numbering, chain A)", af.MonomerEntryID),
	}, nil
}

// BuildMutagenesis is Stage 2. From the acquired base structure it builds a matched
// wild-type/mutant pair by side-chain mutagenesis: the base residue at the target
// position is set to the wild-type residue for the WT track and to the mutant residue
// for the mutant track. Both are written in the same coordinate frame (a clean
// comparison basis, robust to the base crystal already carrying the mutation), under
// the run's structure directory, and served via GET /runs/:id/structure/:track.
func BuildMutagenesis(ctx context.Context, runID, uniprotID string, mutation models.Mutation, siteHint string) (*models.MutagenesisResult, error) {
	wtRes, ok1 := aaThreeLetter[strings.ToUpper(mutation.WildType)]
	mutRes, ok2 := aaThreeLetter[strings.ToUpper(mutation.Mutant)]
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("mutagenesis: unknown amino-acid code in %q", mutation.Raw)
	}

	base, err := resolveBase(uniprotID, mutation, siteHint)
	if err != nil {
		return nil, fmt.Errorf("mutagenesis: %w", err)
	}

	dir := RunStructureDir(runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mutagenesis: create run dir: %w", err)
	}

	basePath := filepath.Join(dir, "base.cif")
	if err := downloadFile(ctx, base.url, basePath); err != nil {
		return nil, fmt.Errorf("mutagenesis: download base structure: %w", err)
	}

	// Build both tracks from the same base so they share a backbone frame. The
	// helper verifies the written residue, so a base whose target position holds
	// neither the wild-type nor the mutant residue fails loudly here rather than
	// yielding a structure silently mutated at the wrong place.
	if err := runMutate(ctx, basePath, base, wtRes, RunStructurePath(runID, "wt")); err != nil {
		return nil, fmt.Errorf("mutagenesis: build WT structure: %w", err)
	}
	if err := runMutate(ctx, basePath, base, mutRes, RunStructurePath(runID, "mutant")); err != nil {
		return nil, fmt.Errorf("mutagenesis: build mutant structure: %w", err)
	}

	notes := []string{
		base.label,
		"WT and mutant share one backbone frame — they differ only at the target residue",
	}
	if base.stripHet {
		notes = append(notes, "bound ligands, ions and water removed; the pocket conformation they induced is kept")
	}

	return &models.MutagenesisResult{
		Tool:               mutagenesisTool,
		WTStructureURL:     fmt.Sprintf("/runs/%s/structure/wt", runID),
		MutantStructureURL: fmt.Sprintf("/runs/%s/structure/mutant", runID),
		TargetChain:        base.chain,
		TargetResidueNum:   base.resnum,
		WildTypeResidue:    wtRes,
		MutantResidue:      mutRes,
		Notes:              notes,
	}, nil
}

// runMutate shells out to the PDBFixer helper for one single-residue mutation.
func runMutate(ctx context.Context, input string, base mutateBase, to, out string) error {
	args := []string{mutateScript,
		"--input", input,
		"--chain", base.chain,
		"--resnum", fmt.Sprint(base.resnum),
		"--to", to,
		"--out", out,
	}
	if base.keepChain != "" {
		args = append(args, "--keep-chain", base.keepChain)
	}
	if base.stripHet {
		args = append(args, "--strip-het")
	}
	cmd := exec.CommandContext(ctx, "python3", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
