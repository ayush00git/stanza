package services

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// 30s timeout absorbs cold-start latency (DNS + TLS handshake) on the first
// outbound request, which previously tripped the 10s limit and made the very
// first search return no results until a warm retry.
var uniprotClient = &http.Client{Timeout: 30 * time.Second}

const uniprotBaseURL = "https://rest.uniprot.org/uniprotkb"

// Retry settings for the UniProt search call. A cold first request can fail
// transiently (cold TLS, brief network blip, a 5xx); without a retry that
// surfaces to the user as an empty result set, fixable only by refreshing.
const (
	uniprotMaxAttempts  = 3
	uniprotRetryBackoff = 300 * time.Millisecond
	// Don't retry a failure that took longer than this. A fast failure (reset,
	// instant 503) is a blip worth retrying; a slow one means UniProt is degraded
	// (it serves slow 503s with retry-after during outages), so retrying just
	// stacks latency toward a gateway timeout.
	uniprotSlowFailCutoff = 2 * time.Second
)

// UniProtEntry matches the fields we need from the UniProt REST API.
// The same shape is returned both by the single-entry endpoint and by each
// element of the search endpoint's results array, so it serves both paths.
type UniProtEntry struct {
	PrimaryAccession   string `json:"primaryAccession"`
	EntryType          string `json:"entryType"` // "Swiss-Prot" (reviewed) or "TrEMBL" (unreviewed)
	ProteinDescription struct {
		RecommendedName struct {
			FullName struct {
				Value string `json:"value"`
			} `json:"fullName"`
		} `json:"recommendedName"`
	} `json:"proteinDescription"`
	Genes []struct {
		GeneName struct {
			Value string `json:"value"`
		} `json:"geneName"`
	} `json:"genes"`
	Organism struct {
		ScientificName string `json:"scientificName"`
		TaxonID        int    `json:"taxonId"`
	} `json:"organism"`
	Comments []struct {
		CommentType string `json:"commentType"`
		Disease     struct {
			DiseaseID   string `json:"diseaseId"`
			Description string `json:"description"`
		} `json:"disease"`
	} `json:"comments"`
}

// FetchUniProtEntry fetches protein metadata from UniProt by accession ID.
// Transient failures (connection errors, 503s — UniProt frequently returns
// "503 Backend fetch failed" from its Varnish edge) are retried; a genuine 404
// is returned immediately without retrying.
func FetchUniProtEntry(uniprotID string) (*UniProtEntry, error) {
	fetchURL := fmt.Sprintf("%s/%s?format=json", uniprotBaseURL, uniprotID)

	var lastErr error
	for attempt := range uniprotMaxAttempts {
		if attempt > 0 {
			time.Sleep(uniprotRetryBackoff)
		}
		start := time.Now()
		entry, retryable, err := fetchUniProtEntryOnce(fetchURL, uniprotID)
		if err == nil {
			return entry, nil
		}
		lastErr = err
		if !retryable || time.Since(start) > uniprotSlowFailCutoff {
			break
		}
	}
	return nil, lastErr
}

// fetchUniProtEntryOnce performs a single entry fetch. The bool reports whether
// the error is worth retrying (true for transient failures, false for a 404).
func fetchUniProtEntryOnce(fetchURL, uniprotID string) (*UniProtEntry, bool, error) {
	resp, err := uniprotClient.Get(fetchURL)
	if err != nil {
		return nil, true, fmt.Errorf("uniprot GET failed for %s: %w", uniprotID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, false, fmt.Errorf("uniprot: accession %s not found", uniprotID)
	}
	if resp.StatusCode != 200 {
		return nil, true, fmt.Errorf("uniprot: status %d for %s", resp.StatusCode, uniprotID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, fmt.Errorf("uniprot: read failed for %s: %w", uniprotID, err)
	}

	// Check if response is gzip-compressed (starts with magic bytes 0x1f 0x8b)
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, true, fmt.Errorf("uniprot: gzip decompress failed: %w", err)
		}
		defer gr.Close()
		decompressed, err := io.ReadAll(gr)
		if err != nil {
			return nil, true, fmt.Errorf("uniprot: gzip read failed: %w", err)
		}
		body = decompressed
	}

	var entry UniProtEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, true, fmt.Errorf("uniprot: parse failed for %s: %w", uniprotID, err)
	}

	return &entry, false, nil
}

// searchFields is the full set of fields we hydrate per result, so a single
// search call returns everything buildComplexForSearch needs — no N+1 per-entry
// follow-up requests to UniProt.
const searchFields = "accession,id,protein_name,gene_names,organism_name,organism_id,cc_disease,reviewed"

// SearchUniProtEntries searches UniProt and returns fully-hydrated entries in a
// single request. This replaces the previous "fetch IDs, then fetch each entry"
// (N+1) pattern: one call now carries protein name, gene, organism, taxon, and
// disease associations for every hit. The only remaining per-protein work is the
// AlphaFold structure lookup, which has no batch endpoint and is streamed.
func SearchUniProtEntries(query string, limit int) ([]*UniProtEntry, error) {
	combined := fmt.Sprintf("(%s) AND (reviewed:true)", query)
	searchURL := fmt.Sprintf("%s/search?query=%s&format=json&size=%d&fields=%s",
		uniprotBaseURL, url.QueryEscape(combined), limit, searchFields)

	// Retry transient failures so a single cold hiccup doesn't surface as "no
	// results". Only the network/HTTP call is retried; a successful response
	// (including a genuinely empty match set) returns immediately.
	var lastErr error
	for attempt := range uniprotMaxAttempts {
		if attempt > 0 {
			time.Sleep(uniprotRetryBackoff)
		}
		start := time.Now()
		entries, err := fetchUniProtSearch(searchURL, query)
		if err == nil {
			return entries, nil
		}
		lastErr = err
		if time.Since(start) > uniprotSlowFailCutoff {
			break
		}
	}
	return nil, lastErr
}

// fetchUniProtSearch performs a single UniProt search request and parses the result.
func fetchUniProtSearch(searchURL, query string) ([]*UniProtEntry, error) {
	resp, err := uniprotClient.Get(searchURL)
	if err != nil {
		return nil, fmt.Errorf("uniprot search failed for '%s': %w", query, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("uniprot search: status %d for '%s'", resp.StatusCode, query)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("uniprot search: read failed: %w", err)
	}

	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("uniprot search: gzip decompress failed: %w", err)
		}
		defer gr.Close()
		decompressed, err := io.ReadAll(gr)
		if err != nil {
			return nil, fmt.Errorf("uniprot search: gzip read failed: %w", err)
		}
		body = decompressed
	}

	var result struct {
		Results []*UniProtEntry `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("uniprot search: parse failed: %w. Raw: %s", err, string(body[:min(200, len(body))]))
	}

	return result.Results, nil
}

// SearchUniProt searches UniProt by query string and returns the top UniProt IDs.
// Used for the search-by-disease or search-by-protein-name feature.
func SearchUniProt(query string, limit int) ([]string, error) {
	// Restrict to Swiss-Prot (reviewed) only — TrEMBL entries are being deprecated
	// by UniProt and have very low AlphaFold coverage. Sort by relevance score.
	combined := fmt.Sprintf("(%s) AND (reviewed:true)", query)
	searchURL := fmt.Sprintf("%s/search?query=%s&format=json&size=%d&fields=accession,id", uniprotBaseURL, url.QueryEscape(combined), limit)

	resp, err := uniprotClient.Get(searchURL)
	if err != nil {
		return nil, fmt.Errorf("uniprot search failed for '%s': %w", query, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("uniprot search: status %d for '%s'", resp.StatusCode, query)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("uniprot search: read failed: %w", err)
	}

	// Check if response is gzip-compressed (starts with magic bytes 0x1f 0x8b)
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("uniprot search: gzip decompress failed: %w", err)
		}
		defer gr.Close()
		decompressed, err := io.ReadAll(gr)
		if err != nil {
			return nil, fmt.Errorf("uniprot search: gzip read failed: %w", err)
		}
		body = decompressed
	}

	var result struct {
		Results []struct {
			PrimaryAccession string `json:"primaryAccession"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("uniprot search: parse failed: %w. Raw: %s", err, string(body[:min(200, len(body))]))
	}

	var ids []string
	for _, r := range result.Results {
		ids = append(ids, r.PrimaryAccession)
	}
	return ids, nil
}
