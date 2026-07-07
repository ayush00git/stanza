package services

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var plddtClient = &http.Client{Timeout: 15 * time.Second}

// confidenceJSON matches the AlphaFold per-residue confidence JSON format.
type confidenceJSON struct {
	ResidueNumber   []int     `json:"residueNumber"`
	ConfidenceScore []float64 `json:"confidenceScore"`
}

// FetchMonomerPLDDT fetches per-residue pLDDT data exclusively for the monomer form.
func FetchMonomerPLDDT(monomerEntryID string) (map[int]float64, error) {
	if monomerEntryID == "" {
		return nil, fmt.Errorf("plddt: empty monomer entry ID")
	}

	monomerConf, err := fetchConfidenceJSON(monomerEntryID)
	if err != nil {
		return nil, fmt.Errorf("plddt: monomer fetch failed: %w", err)
	}

	// Build monomer lookup: residue index → pLDDT
	monomerMap := make(map[int]float64)
	for i, resIdx := range monomerConf.ResidueNumber {
		if i < len(monomerConf.ConfidenceScore) {
			monomerMap[resIdx] = monomerConf.ConfidenceScore[i]
		}
	}

	return monomerMap, nil
}

// fetchConfidenceJSON downloads the AlphaFold per-residue confidence JSON.
// Tries version 4 first, then falls back to version 3 and 2.
func fetchConfidenceJSON(entryID string) (*confidenceJSON, error) {
	versions := []int{6, 5, 4, 3, 2, 1}
	var lastErr error

	for _, v := range versions {
		url := fmt.Sprintf("https://alphafold.ebi.ac.uk/files/%s-confidence_v%d.json", entryID, v)
		conf, err := doFetchConfidence(url)
		if err != nil {
			lastErr = err
			continue
		}
		return conf, nil
	}

	return nil, fmt.Errorf("plddt: all version attempts failed for %s: %w", entryID, lastErr)
}

func doFetchConfidence(url string) (*confidenceJSON, error) {
	resp, err := plddtClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("not found: %s", url)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip decompress failed: %w", err)
		}
		defer gr.Close()
		reader = gr
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}

	// Try object format: { "residueNumber": [...], "confidenceScore": [...] }
	var conf confidenceJSON
	if err := json.Unmarshal(body, &conf); err == nil && len(conf.ResidueNumber) > 0 {
		return &conf, nil
	}

	// Try array-of-objects format: [{ "residueNumber": N, "confidenceScore": S }, ...]
	var arrayFormat []struct {
		ResidueNumber   int     `json:"residueNumber"`
		ConfidenceScore float64 `json:"confidenceScore"`
	}
	if err := json.Unmarshal(body, &arrayFormat); err == nil && len(arrayFormat) > 0 {
		for _, entry := range arrayFormat {
			conf.ResidueNumber = append(conf.ResidueNumber, entry.ResidueNumber)
			conf.ConfidenceScore = append(conf.ConfidenceScore, entry.ConfidenceScore)
		}
		return &conf, nil
	}

	preview := string(body)
	if len(preview) > 200 {
		preview = preview[:200]
	}
	return nil, fmt.Errorf("unable to parse confidence JSON for %s. Preview: %s", url, preview)
}
