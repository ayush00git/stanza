package services

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// 30s timeout absorbs cold-start latency (DNS + TLS handshake) on the first
// outbound request, which otherwise trips a tighter limit and makes the very
// first request fail until a warm retry.
var uniprotClient = &http.Client{Timeout: 30 * time.Second}

const uniprotBaseURL = "https://rest.uniprot.org/uniprotkb"

// Retry settings for the UniProt call. A cold first request can fail transiently
// (cold TLS, a brief network blip, a 5xx); without a retry that surfaces as an
// empty result, fixable only by refreshing.
const (
	uniprotMaxAttempts  = 3
	uniprotRetryBackoff = 300 * time.Millisecond
	// Don't retry a failure that took longer than this. A fast failure (reset,
	// instant 503) is a blip worth retrying; a slow one means UniProt is degraded
	// (it serves slow 503s with retry-after during outages), so retrying just
	// stacks latency toward a gateway timeout.
	uniprotSlowFailCutoff = 2 * time.Second
)

// UniProtEntry maps the fields we need from the UniProt REST API. The subunit
// (monomer/homodimer) annotation lives in a comment of type SUBUNIT.
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
	Sequence struct {
		Value  string `json:"value"`
		Length int    `json:"length"`
	} `json:"sequence"`
	Comments []struct {
		CommentType string `json:"commentType"`
		// SUBUNIT comments carry the monomer/homodimer text in texts[].value.
		Texts []struct {
			Value string `json:"value"`
		} `json:"texts"`
		Disease struct {
			DiseaseID   string `json:"diseaseId"`
			Description string `json:"description"`
		} `json:"disease"`
	} `json:"comments"`
}

// SubunitStructure returns the free-text quaternary-structure annotation
// (e.g. "Homodimer", "Monomer") from the SUBUNIT comment, or "" if absent.
func (e *UniProtEntry) SubunitStructure() string {
	for _, c := range e.Comments {
		if c.CommentType == "SUBUNIT" && len(c.Texts) > 0 {
			return c.Texts[0].Value
		}
	}
	return ""
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
