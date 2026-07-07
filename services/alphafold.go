package services

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// 30s timeout absorbs cold-start latency (DNS + TLS handshake) on the first
// outbound request so a cold first search doesn't fail before warming up.
var alphafoldClient = &http.Client{Timeout: 30 * time.Second}

const alphafoldBaseURL = "https://alphafold.ebi.ac.uk/api"

// AlphaFoldPrediction matches the shape of the AlphaFold API /prediction/{id} response.
// IMPORTANT: Only fields we actually use are mapped. Do not add unmapped fields.
type AlphaFoldPrediction struct {
	EntryID           string  `json:"entryId"`
	UniprotAcc        string  `json:"uniprotAccession"`
	Gene              string  `json:"gene"`
	Description       string  `json:"uniprotDescription"`
	TaxID             int     `json:"taxId"`
	OrgName           string  `json:"organismsScientificName"`
	CifURL            string  `json:"cifUrl"`
	PdbURL            string  `json:"pdbUrl"`
	GlobalMetricValue float64 `json:"globalMetricValue"`
}

// FetchMonomerPrediction calls the AlphaFold API for a single UniProt ID.
// Returns the first prediction in the array response.
// Returns error if the response is not a valid array or is empty.
func FetchMonomerPrediction(uniprotID string) (*AlphaFoldPrediction, error) {
	url := fmt.Sprintf("%s/prediction/%s", alphafoldBaseURL, uniprotID)
	resp, err := alphafoldClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("alphafold GET failed for %s: %w", uniprotID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("alphafold: no prediction found for UniProt ID %s", uniprotID)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("alphafold: unexpected status %d for %s", resp.StatusCode, uniprotID)
	}

	// Handle gzip-compressed responses
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("alphafold: gzip decompress failed: %w", err)
		}
		defer gr.Close()
		reader = gr
	}

	body, err := io.ReadAll(reader)
	var predictions []AlphaFoldPrediction
	if err := json.Unmarshal(body, &predictions); err != nil {
		return nil, fmt.Errorf("alphafold: failed to parse response for %s: %w. Raw: %s", uniprotID, err, string(body[:min(200, len(body))]))
	}

	if len(predictions) == 0 {
		return nil, fmt.Errorf("alphafold: empty predictions array for %s", uniprotID)
	}

	return &predictions[0], nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type ComplexData struct {
	MonomerPLDDT   float64
	DimerPLDDT     float64
	DisorderDelta  float64
	MonomerEntryID string
	MonomerCifURL  string
	ComplexEntryID string
	ComplexCifURL  string
	IpTMScore      float64
}

type alphaFoldSearchResponse struct {
	NumFound int `json:"numFound"`
	Docs     []struct {
		IsComplex         bool    `json:"isComplex"`
		IsIsoform         bool    `json:"isIsoform"`
		GlobalMetricValue float64 `json:"globalMetricValue"`
		EntryID           string  `json:"entryId"`
		ModelEntityID     string  `json:"modelEntityId"`
		LatestVersion     int     `json:"latestVersion"`
		IpTM              float64 `json:"complexPredictionAccuracy_ipTM"`
	} `json:"docs"`
}

func FetchComplexData(uniprotID string) (*ComplexData, error) {
	urlStr := fmt.Sprintf("%s/search?q=%s&type=complex", alphafoldBaseURL, uniprotID)
	resp, err := alphafoldClient.Get(urlStr)
	if err != nil {
		return nil, fmt.Errorf("alphafold search GET failed for %s: %w", uniprotID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("alphafold search: unexpected status %d for %s", resp.StatusCode, uniprotID)
	}

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("alphafold search: gzip decompress failed: %w", err)
		}
		defer gr.Close()
		reader = gr
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("alphafold search read failed: %w", err)
	}

	var searchResp alphaFoldSearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, fmt.Errorf("alphafold search JSON parse failed for %s: %w. Raw: %s", uniprotID, err, string(body[:min(200, len(body))]))
	}

	res := &ComplexData{}
	hasMonomer := false
	hasComplex := false

	for _, doc := range searchResp.Docs {
		// Use ModelEntityID if available, otherwise fall back to EntryID
		effectiveID := doc.EntryID
		if doc.ModelEntityID != "" {
			effectiveID = doc.ModelEntityID
		}

		if !doc.IsComplex && !doc.IsIsoform {
			hasMonomer = true
			res.MonomerPLDDT = doc.GlobalMetricValue
			res.MonomerEntryID = effectiveID
			res.MonomerCifURL = fmt.Sprintf("https://alphafold.ebi.ac.uk/files/%s-model_v%d.cif", effectiveID, doc.LatestVersion)
		} else if doc.IsComplex {
			hasComplex = true
			res.DimerPLDDT = doc.GlobalMetricValue
			res.ComplexEntryID = effectiveID
			res.ComplexCifURL = fmt.Sprintf("https://alphafold.ebi.ac.uk/files/%s-model_v%d.cif", effectiveID, doc.LatestVersion)
			res.IpTMScore = doc.IpTM
		}
	}

	if !hasMonomer {
		return nil, fmt.Errorf("alphafold search: no monomer found for %s", uniprotID)
	}

	if !hasComplex {
		res.DimerPLDDT = res.MonomerPLDDT
		res.DisorderDelta = 0
		res.ComplexEntryID = ""
		res.ComplexCifURL = ""
	} else {
		res.DisorderDelta = res.DimerPLDDT - res.MonomerPLDDT
		if res.DisorderDelta < 0 {
			res.DisorderDelta = 0
		}
	}

	return res, nil
}
