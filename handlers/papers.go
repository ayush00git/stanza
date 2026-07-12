package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ayush00git/stanza/models"
	"github.com/ayush00git/stanza/services"
)

// maxPaperBytes caps an uploaded PDF at 32 MB. The extraction ships the bytes to
// Claude, so an unbounded upload is both a memory and a cost hazard.
const maxPaperBytes = 32 << 20 // 32 MB

// readUploadedPDF pulls the multipart "file" field, enforces the size ceiling, the .pdf
// suffix, and the %PDF magic, and returns the bytes and filename. On any failure it writes
// the error response itself and returns ok=false, so a caller just returns on !ok.
func readUploadedPDF(c *gin.Context) (data []byte, filename string, ok bool) {
	header, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a PDF must be uploaded in the multipart form field 'file'"})
		return nil, "", false
	}
	if header.Size > maxPaperBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf("file is %d bytes; the limit is %d bytes (32 MB)", header.Size, maxPaperBytes)})
		return nil, "", false
	}
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".pdf") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file must be a .pdf"})
		return nil, "", false
	}
	f, err := header.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("could not open the uploaded file: %v", err)})
		return nil, "", false
	}
	defer f.Close()
	// LimitReader guards against a header.Size that understates the real body.
	data, err = io.ReadAll(io.LimitReader(f, maxPaperBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("could not read the uploaded file: %v", err)})
		return nil, "", false
	}
	if len(data) > maxPaperBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file exceeds the 32 MB limit"})
		return nil, "", false
	}
	// The ".pdf" suffix is a claim; the "%PDF" magic is proof.
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is not a PDF (missing %PDF magic header)"})
		return nil, "", false
	}
	return data, header.Filename, true
}

// ExtractPaperHandler handles POST /papers/extract. It takes an uploaded PDF from
// the multipart "file" field, hands the bytes to Claude, and returns a proposed
// models.ExtractedSite draft for a human to review. Nothing here touches a run:
// the draft is confirmed separately via POST /papers/confirm.
//
// Response shape: gin.H{"extraction": site}. Prefer ExtractPaperStreamHandler for the UI;
// this blocking form is kept for scripting and non-streaming clients.
func ExtractPaperHandler(c *gin.Context) {
	data, filename, ok := readUploadedPDF(c)
	if !ok {
		return
	}
	site, err := services.ExtractSiteFromPDF(c.Request.Context(), data, filename)
	if err != nil {
		// The extraction is an upstream Claude call, so a failure here is a bad gateway.
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"extraction": site})
}

// ExtractPaperStreamHandler handles POST /papers/extract/stream (SSE over POST). It reads
// the PDF the same way, then streams Claude's summarized reasoning as it works through the
// paper before delivering the final draft. The Claude call is a minute or two, and watching
// the model decide the reactive residue is far better than a spinner.
//
// Events: `progress` (models-free {stage, thinking}) → `extraction` ({extraction: site}) →
// `done`, or a single `error`. It is a POST because the payload is a file upload, so the
// client reads the response body as a stream rather than using EventSource.
func ExtractPaperStreamHandler(c *gin.Context) {
	data, filename, ok := readUploadedPDF(c)
	if !ok {
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}
	w := c.Writer
	send := func(name string, payload any) {
		b, err := json.Marshal(payload)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
		flusher.Flush()
	}

	// The stream runs on this goroutine; onProgress fires inline, so writing and flushing
	// directly from the callback is safe with no channel needed.
	site, err := services.ExtractSiteFromPDFStream(c.Request.Context(), data, filename,
		func(p services.PaperProgress) { send("progress", p) })
	if err != nil {
		send("error", gin.H{"error": err.Error()})
		return
	}
	send("extraction", gin.H{"extraction": site})
	send("done", gin.H{})
}

// ConfirmPaperHandler handles POST /papers/confirm. It accepts a user-edited,
// confirmed models.ExtractedSite draft, validates the minimum a run needs, and
// registers it in the runtime site registry so a subsequent POST /runs against
// this uniprot_id + mutation drives off the confirmed site.
//
// Response shape: gin.H{"uniprot_id": ..., "mutation": ...}.
func ConfirmPaperHandler(c *gin.Context) {
	if ct := c.GetHeader("Content-Type"); !strings.Contains(strings.ToLower(ct), "application/json") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Content-Type must be application/json"})
		return
	}

	var site models.ExtractedSite
	if err := json.NewDecoder(c.Request.Body).Decode(&site); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return
	}

	site.UniprotID = strings.TrimSpace(site.UniprotID)
	site.Mutation = strings.TrimSpace(site.Mutation)

	// Validate only what it takes to drive a run: an identity and a mutation.
	var missing []string
	if site.UniprotID == "" {
		missing = append(missing, "uniprot_id")
	}
	if site.Mutation == "" {
		missing = append(missing, "mutation")
	}
	if len(missing) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("missing required field(s): %s", strings.Join(missing, ", "))})
		return
	}

	services.RegisterExtractedSite(&site)

	c.JSON(http.StatusOK, gin.H{
		"uniprot_id": site.UniprotID,
		"mutation":   site.Mutation,
	})
}
