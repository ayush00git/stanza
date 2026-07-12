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

// ExtractPaperHandler handles POST /papers/extract. It takes an uploaded PDF from
// the multipart "file" field, hands the bytes to Claude, and returns a proposed
// models.ExtractedSite draft for a human to review. Nothing here touches a run:
// the draft is confirmed separately via POST /papers/confirm.
//
// Response shape: gin.H{"extraction": site}.
func ExtractPaperHandler(c *gin.Context) {
	header, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a PDF must be uploaded in the multipart form field 'file'"})
		return
	}

	// Size guard first: refuse to read a file we would only reject.
	if header.Size > maxPaperBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf("file is %d bytes; the limit is %d bytes (32 MB)", header.Size, maxPaperBytes)})
		return
	}

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".pdf") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file must be a .pdf"})
		return
	}

	f, err := header.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("could not open the uploaded file: %v", err)})
		return
	}
	defer f.Close()

	// LimitReader guards against a header.Size that understates the real body.
	data, err := io.ReadAll(io.LimitReader(f, maxPaperBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("could not read the uploaded file: %v", err)})
		return
	}
	if len(data) > maxPaperBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file exceeds the 32 MB limit"})
		return
	}

	// Content guard: the ".pdf" suffix is a claim; the "%PDF" magic is proof.
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is not a PDF (missing %PDF magic header)"})
		return
	}

	site, err := services.ExtractSiteFromPDF(c.Request.Context(), data, header.Filename)
	if err != nil {
		// The extraction is an upstream Claude call, so a failure here is a bad gateway.
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"extraction": site})
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
