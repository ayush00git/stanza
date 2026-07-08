package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 30s timeout mirrors the UniProt/AlphaFold clients so a cold first request
// (DNS + TLS handshake) doesn't fail before the connection warms up.
var sequenceClient = &http.Client{Timeout: 30 * time.Second}

// FetchUniProtSequence returns the canonical amino-acid sequence for a UniProt
// accession. It fetches the FASTA record, strips the ">" header line and
// concatenates the sequence lines (uppercase, no whitespace).
func FetchUniProtSequence(ctx context.Context, uniprotID string) (string, error) {
	fetchURL := fmt.Sprintf("%s/%s.fasta", uniprotBaseURL, uniprotID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return "", fmt.Errorf("uniprot fasta: build request failed for %s: %w", uniprotID, err)
	}

	resp, err := sequenceClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("uniprot fasta GET failed for %s: %w", uniprotID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("uniprot fasta: accession %s not found", uniprotID)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("uniprot fasta: status %d for %s", resp.StatusCode, uniprotID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("uniprot fasta: read failed for %s: %w", uniprotID, err)
	}

	// UniProt occasionally serves gzip (magic bytes 0x1f 0x8b) that the transport
	// didn't transparently decompress; mirror the handling in uniprot.go.
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("uniprot fasta: gzip decompress failed for %s: %w", uniprotID, err)
		}
		defer gr.Close()
		decompressed, err := io.ReadAll(gr)
		if err != nil {
			return "", fmt.Errorf("uniprot fasta: gzip read failed for %s: %w", uniprotID, err)
		}
		body = decompressed
	}

	var sb strings.Builder
	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ">") {
			continue // skip the FASTA header line(s) and blank lines
		}
		sb.WriteString(strings.ToUpper(line))
	}

	seq := sb.String()
	if seq == "" {
		return "", fmt.Errorf("uniprot fasta: empty sequence for %s", uniprotID)
	}
	return seq, nil
}
