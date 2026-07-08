package services

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ayush00git/stanza/models"
)

var fpocketClient = &http.Client{Timeout: 30 * time.Second}

// RunFpocket downloads a structure file from the given URL, runs fpocket on it,
// parses the output, and returns a list of identified pockets sorted by
// druggability score descending.
func RunFpocket(structureURL string) ([]models.Pocket, error) {
	if structureURL == "" {
		return nil, fmt.Errorf("fpocket: empty structure URL")
	}

	localTmpParent := "./tmp"
	if err := os.MkdirAll(localTmpParent, 0755); err != nil {
		return nil, fmt.Errorf("fpocket: failed to create tmp dir: %w", err)
	}

	tmpDir, err := os.MkdirTemp(localTmpParent, "fpocket-*")
	if err != nil {
		return nil, fmt.Errorf("fpocket: failed to create run dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	isCif := strings.HasSuffix(strings.ToLower(structureURL), ".cif")
	pdbURL := structureURL
	if isCif {
		pdbURL = structureURL[:len(structureURL)-4] + ".pdb"
	}

	structurePath := filepath.Join(tmpDir, "structure.pdb")
	err = downloadAndVerify(pdbURL, structurePath)
	if err != nil && isCif {
		structurePath = filepath.Join(tmpDir, "structure.cif")
		err = downloadAndVerify(structureURL, structurePath)
	}
	if err != nil {
		return nil, fmt.Errorf("fpocket: download failed: %w", err)
	}

	info, statErr := os.Stat(structurePath)
	if statErr != nil || info.Size() == 0 {
		return nil, fmt.Errorf("fpocket: downloaded file missing or empty at %s", structurePath)
	}

	return analyzeStructureFile(structurePath)
}

// RunFpocketFile runs fpocket on an existing LOCAL structure file (e.g. a
// generated wt/mutant PDB) and returns pockets sorted by druggability.
func RunFpocketFile(structurePath string) ([]models.Pocket, error) {
	if structurePath == "" {
		return nil, fmt.Errorf("fpocket: empty structure path")
	}

	info, statErr := os.Stat(structurePath)
	if statErr != nil || info.Size() == 0 {
		return nil, fmt.Errorf("fpocket: structure file missing or empty at %s", structurePath)
	}

	localTmpParent := "./tmp"
	if err := os.MkdirAll(localTmpParent, 0755); err != nil {
		return nil, fmt.Errorf("fpocket: failed to create tmp dir: %w", err)
	}

	tmpDir, err := os.MkdirTemp(localTmpParent, "fpocket-*")
	if err != nil {
		return nil, fmt.Errorf("fpocket: failed to create run dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Copy the input file into the tmp dir so fpocket's `_out` directory is
	// written next to our copy rather than littering the caller's directory.
	copiedPath := filepath.Join(tmpDir, "structure"+filepath.Ext(structurePath))
	if err := copyFile(structurePath, copiedPath); err != nil {
		return nil, fmt.Errorf("fpocket: failed to copy structure file: %w", err)
	}

	return analyzeStructureFile(copiedPath)
}

// analyzeStructureFile runs fpocket on a local structure file that already sits
// in a writable directory, parses its output, and returns the enriched pockets
// sorted by druggability. The caller owns creating/cleaning the directory.
func analyzeStructureFile(structurePath string) ([]models.Pocket, error) {
	workDir := filepath.Dir(structurePath)

	// Run fpocket
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "fpocket", "-f", filepath.Base(structurePath))
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("fpocket: execution failed: %w. Output: %s", err, string(output))
	}

	outDir := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(structurePath), filepath.Ext(structurePath)) + "_out")
	if _, err := os.Stat(outDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("fpocket: output directory not found")
	}

	infoPath := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(structurePath), filepath.Ext(structurePath)) + "_info.txt")
	pockets, err := parseFpocketInfo(infoPath)
	if err != nil {
		return nil, fmt.Errorf("fpocket: failed to parse info: %w", err)
	}

	originalPLDDT, _ := parseOriginalPDBConfidences(structurePath)
	if originalPLDDT == nil {
		originalPLDDT = make(map[string]float64)
	}

	// Process each pocket
	for i := range pockets {
		pocketAtmPath := filepath.Join(outDir, "pockets", fmt.Sprintf("pocket%d_atm.pdb", pockets[i].PocketID))
		resIndices, resNames, chains, resChains, _, _, err := parsePocketAtoms(pocketAtmPath)
		if err != nil {
			continue
		}

		pockets[i].ResidueIndices = resIndices
		pockets[i].ResidueNames = resNames
		pockets[i].ResidueChains = resChains
		pockets[i].Chains = chains

		// Compute center from original PDB coordinates
		origCoords := getResiduesCoordsFromOriginal(structurePath, resIndices, resChains)
		pockets[i].Center = computeCenter(origCoords)

		localPLDDT := make(map[string]float64)
		for j, idx := range resIndices {
			key := fmt.Sprintf("%s:%d", resChains[j], idx)
			localPLDDT[key] = originalPLDDT[key]
		}
		pockets[i].LocalPLDDT = localPLDDT
		pockets[i].IsInterfacePocket = len(chains) > 1

		var plddtSum float64
		var plddtCount int
		pockets[i].ResidueConfidences = make([]models.ResidueConfidence, 0, len(resIndices))
		for j, idx := range resIndices {
			key := fmt.Sprintf("%s:%d", resChains[j], idx)
			val := localPLDDT[key]
			if val > 0 {
				plddtSum += val
				plddtCount++
			}
			pockets[i].ResidueConfidences = append(pockets[i].ResidueConfidences, models.ResidueConfidence{
				ResidueIndex: idx,
				Chain:        resChains[j],
				MonomerPLDDT: val,
				DimerPLDDT:   val,
				Delta:        0.0,
			})
		}
		if plddtCount > 0 {
			pockets[i].AvgPLDDT = plddtSum / float64(plddtCount)
		}
	}

	// Sort by druggability score
	sort.Slice(pockets, func(i, j int) bool {
		return pockets[i].Score > pockets[j].Score
	})

	return pockets, nil
}

// ---------------- Helper Functions ----------------

// copyFile copies the contents of src into a new file at dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source failed: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("file create failed: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return fmt.Errorf("copy failed: %w", err)
	}
	return out.Close()
}

func downloadAndVerify(url, destPath string) error {
	resp, err := fpocketClient.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("file create failed: %w", err)
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil || n == 0 {
		os.Remove(destPath)
		return fmt.Errorf("download failed for %s: %w", url, err)
	}
	return nil
}

func parseFpocketInfo(infoPath string) ([]models.Pocket, error) {
	f, err := os.Open(infoPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var pockets []models.Pocket
	var current *models.Pocket
	scanner := bufio.NewScanner(f)
	pocketHeaderRe := regexp.MustCompile(`^Pocket\s+(\d+)\s*:`)
	kvRe := regexp.MustCompile(`^\s+(.+?)\s*:\s+(.+)$`)

	for scanner.Scan() {
		line := scanner.Text()
		if m := pocketHeaderRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				pockets = append(pockets, *current)
			}
			id, _ := strconv.Atoi(m[1])
			current = &models.Pocket{PocketID: id}
			continue
		}
		if current == nil {
			continue
		}
		if m := kvRe.FindStringSubmatch(line); m != nil {
			key := strings.TrimSpace(m[1])
			valStr := strings.TrimSpace(m[2])
			switch key {
			case "Druggability Score":
				current.Score, _ = strconv.ParseFloat(valStr, 64)
			case "Score":
				if current.Score == 0 {
					current.Score, _ = strconv.ParseFloat(valStr, 64)
				}
			case "Volume":
				current.Volume, _ = strconv.ParseFloat(valStr, 64)
			case "Total SASA":
				current.SurfaceArea, _ = strconv.ParseFloat(valStr, 64)
			case "Mean alpha sphere radius", "Mean alpha-sphere radius":
				current.Depth, _ = strconv.ParseFloat(valStr, 64)
			case "Hydrophobicity score":
				current.Hydrophobicity, _ = strconv.ParseFloat(valStr, 64)
			case "Polarity score":
				current.Polarity, _ = strconv.ParseFloat(valStr, 64)
			}
		}
	}
	if current != nil {
		pockets = append(pockets, *current)
	}
	return pockets, scanner.Err()
}

func parsePocketAtoms(pdbPath string) ([]int, []string, []string, []string, [3]float64, map[string]float64, error) {
	f, err := os.Open(pdbPath)
	if err != nil {
		return nil, nil, nil, nil, [3]float64{}, nil, err
	}
	defer f.Close()

	type ResKey struct {
		Seq   int
		Chain string
		Name  string
	}
	seenResidues := make(map[string]ResKey)
	seenChains := make(map[string]bool)
	resBfactors := make(map[string][]float64)
	var sumX, sumY, sumZ float64
	atomCount := 0
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 54 {
			continue
		}
		recType := strings.TrimSpace(line[:6])
		if recType != "ATOM" && recType != "HETATM" {
			continue
		}
		resName := strings.TrimSpace(line[17:20])
		chainID := strings.TrimSpace(line[21:22])
		resSeq, _ := strconv.Atoi(strings.TrimSpace(line[22:26]))
		key := fmt.Sprintf("%s:%d", chainID, resSeq)
		seenResidues[key] = ResKey{Seq: resSeq, Chain: chainID, Name: resName}
		seenChains[chainID] = true

		if len(line) >= 66 {
			bfactor, _ := strconv.ParseFloat(strings.TrimSpace(line[60:66]), 64)
			resBfactors[key] = append(resBfactors[key], bfactor)
		}
		x, _ := strconv.ParseFloat(strings.TrimSpace(line[30:38]), 64)
		y, _ := strconv.ParseFloat(strings.TrimSpace(line[38:46]), 64)
		z, _ := strconv.ParseFloat(strings.TrimSpace(line[46:54]), 64)
		sumX += x
		sumY += y
		sumZ += z
		atomCount++
	}

	if atomCount == 0 {
		return nil, nil, nil, nil, [3]float64{}, nil, fmt.Errorf("no atoms in %s", pdbPath)
	}

	var keys []string
	for k := range seenResidues {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var indices []int
	var names []string
	var resChains []string
	for _, k := range keys {
		r := seenResidues[k]
		indices = append(indices, r.Seq)
		names = append(names, r.Name)
		resChains = append(resChains, r.Chain)
	}

	var chains []string
	for ch := range seenChains {
		chains = append(chains, ch)
	}
	sort.Strings(chains)

	plddt := make(map[string]float64)
	for key, bfs := range resBfactors {
		var sum float64
		for _, b := range bfs {
			sum += b
		}
		plddt[key] = sum / float64(len(bfs))
	}

	center := [3]float64{sumX / float64(atomCount), sumY / float64(atomCount), sumZ / float64(atomCount)}
	return indices, names, chains, resChains, center, plddt, scanner.Err()
}

func parseOriginalPDBConfidences(pdbPath string) (map[string]float64, error) {
	f, err := os.Open(pdbPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	resBfactors := make(map[string][]float64)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 66 {
			continue
		}
		recType := strings.TrimSpace(line[:6])
		if recType != "ATOM" && recType != "HETATM" {
			continue
		}
		resSeq, _ := strconv.Atoi(strings.TrimSpace(line[22:26]))
		chainID := strings.TrimSpace(line[21:22])
		key := fmt.Sprintf("%s:%d", chainID, resSeq)
		bfactor, _ := strconv.ParseFloat(strings.TrimSpace(line[60:66]), 64)
		resBfactors[key] = append(resBfactors[key], bfactor)
	}

	plddtMap := make(map[string]float64)
	for key, bfs := range resBfactors {
		var sum float64
		for _, b := range bfs {
			sum += b
		}
		plddtMap[key] = sum / float64(len(bfs))
	}
	return plddtMap, scanner.Err()
}

// Computes center of given coordinates
func computeCenter(coords [][3]float64) [3]float64 {
	if len(coords) == 0 {
		return [3]float64{}
	}
	var sumX, sumY, sumZ float64
	for _, c := range coords {
		sumX += c[0]
		sumY += c[1]
		sumZ += c[2]
	}
	n := float64(len(coords))
	return [3]float64{sumX / n, sumY / n, sumZ / n}
}

// Extracts 3D coordinates for given residues from original PDB
func getResiduesCoordsFromOriginal(pdbPath string, indices []int, chains []string) [][3]float64 {
	f, err := os.Open(pdbPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	resSet := make(map[string]bool)
	for i, idx := range indices {
		key := fmt.Sprintf("%s:%d", chains[i], idx)
		resSet[key] = true
	}

	var coords [][3]float64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 54 {
			continue
		}
		recType := strings.TrimSpace(line[:6])
		if recType != "ATOM" && recType != "HETATM" {
			continue
		}
		resSeq, _ := strconv.Atoi(strings.TrimSpace(line[22:26]))
		chainID := strings.TrimSpace(line[21:22])
		key := fmt.Sprintf("%s:%d", chainID, resSeq)
		if !resSet[key] {
			continue
		}
		x, _ := strconv.ParseFloat(strings.TrimSpace(line[30:38]), 64)
		y, _ := strconv.ParseFloat(strings.TrimSpace(line[38:46]), 64)
		z, _ := strconv.ParseFloat(strings.TrimSpace(line[46:54]), 64)
		coords = append(coords, [3]float64{x, y, z})
	}
	return coords
}