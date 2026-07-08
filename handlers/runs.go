package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/ayush00git/stanza/models"
	"github.com/ayush00git/stanza/services"
)

// createRunBody is the POST /runs request payload.
type createRunBody struct {
	UniprotID string `json:"uniprot_id"`
	Mutation  string `json:"mutation"`
	SiteHint  string `json:"site_hint"`
}

// CreateRunHandler handles POST /runs. It parses the mutation, runs Stage-1
// wild-type structure acquisition, stores the run, and responds 201 with it.
func CreateRunHandler(c *gin.Context) {
	if ct := c.GetHeader("Content-Type"); !strings.Contains(strings.ToLower(ct), "application/json") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Content-Type must be application/json"})
		return
	}

	var body createRunBody
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return
	}

	rawID := strings.TrimSpace(body.UniprotID)
	rawMutation := strings.TrimSpace(body.Mutation)
	if rawID == "" || rawMutation == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "uniprot_id and mutation are required"})
		return
	}

	// Accept an AlphaFold ID (e.g. AF-P04637-F1) in uniprot_id, same as /complex.
	uniprotID := normalizeToUniProtID(rawID)

	mutation, err := services.ParseMutation(rawMutation)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	run := &models.Run{
		ID:        uuid.NewString(),
		UniprotID: uniprotID,
		Mutation:  mutation,
		SiteHint:  strings.TrimSpace(body.SiteHint),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// Stage-1 structure acquisition. A hard error is recorded on the run rather
	// than dropped, and we still return 201 with the run body (Status=="error")
	// so the caller can inspect exactly what failed in a single, uniform shape.
	result, err := services.AcquireWTStructure(c.Request.Context(), uniprotID, mutation)
	if err != nil {
		run.Status = "error"
		run.Error = err.Error()
	} else {
		run.Status = "structure_acquired"
		run.WTStructure = result
	}

	DefaultRunStore.Put(run)
	c.JSON(http.StatusCreated, run)
}

// GetRunHandler handles GET /runs/:id.
func GetRunHandler(c *gin.Context) {
	id := c.Param("id")
	run, ok := DefaultRunStore.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	c.JSON(http.StatusOK, run)
}

// ListRunsHandler handles GET /runs, returning all runs newest-first.
func ListRunsHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"runs": DefaultRunStore.List()})
}
