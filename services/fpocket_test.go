package services

import (
	"os"
	"path/filepath"
	"testing"
)

// Helper to create temp files for testing
func createTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file %s: %v", path, err)
	}
	return path
}

func TestParseFpocketInfo(t *testing.T) {
	tmpDir := t.TempDir()
	
	infoContent := `
Pocket 1 :
	Score :          1.234
	Druggability Score :   0.850
	Number of Alpha Spheres :    50
	Total SASA :      300.00
	Polar SASA :       100.00
	Apolar SASA :     200.00
	Volume :          600.50
	Hydrophobicity score :      0.75
	Polarity score :           -0.25

Pocket 2 :
	Score :          0.500
	Druggability Score :   0.120
	Number of Alpha Spheres :    20
	Volume :          150.00
	Hydrophobicity score :     -0.50
	Polarity score :            0.80
`
	infoPath := createTempFile(t, tmpDir, "structure_info.txt", infoContent)

	pockets, err := parseFpocketInfo(infoPath)
	if err != nil {
		t.Fatalf("parseFpocketInfo failed: %v", err)
	}

	if len(pockets) != 2 {
		t.Fatalf("expected 2 pockets, got %d", len(pockets))
	}

	p1 := pockets[0]
	if p1.PocketID != 1 || p1.Score != 0.850 || p1.Volume != 600.50 || p1.Hydrophobicity != 0.75 || p1.Polarity != -0.25 {
		t.Errorf("pocket 1 parsed incorrectly: %+v", p1)
	}

	p2 := pockets[1]
	if p2.PocketID != 2 || p2.Score != 0.120 || p2.Volume != 150.00 || p2.Hydrophobicity != -0.50 || p2.Polarity != 0.80 {
		t.Errorf("pocket 2 parsed incorrectly: %+v", p2)
	}
}

func TestParsePocketAtoms(t *testing.T) {
	tmpDir := t.TempDir()

	// A minimal mock PDB file with 3 atoms across 2 residues
	pdbContent := `
ATOM      1  N   ALA A   1      10.000  20.000  30.000  1.00  0.00           N  
ATOM      2  CA  ALA A   1      11.000  21.000  31.000  1.00  0.00           C  
ATOM      3  N   GLY A   5      20.000  30.000  40.000  1.00  0.00           N  
`
	pdbPath := createTempFile(t, tmpDir, "pocket1_atm.pdb", pdbContent)

	indices, names, chains, _, center, _, err := parsePocketAtoms(pdbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(indices) != 2 || indices[0] != 1 || indices[1] != 5 {
		t.Errorf("expected indices [1, 5], got %v", indices)
	}

	if len(names) != 2 || names[0] != "ALA" || names[1] != "GLY" {
		t.Errorf("expected names [ALA, GLY], got %v", names)
	}

	// Add check for chains
	if len(chains) != 1 || chains[0] != "A" {
		t.Errorf("expected chains [A], got %v", chains)
	}

	expectedX := (10.0 + 11.0 + 20.0) / 3.0
	expectedY := (20.0 + 21.0 + 30.0) / 3.0
	expectedZ := (30.0 + 31.0 + 40.0) / 3.0

	if center[0] != expectedX || center[1] != expectedY || center[2] != expectedZ {
		t.Errorf("expected center [%.3f, %.3f, %.3f], got %v", expectedX, expectedY, expectedZ, center)
	}
}
