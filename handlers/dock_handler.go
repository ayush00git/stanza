package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ayush00git/stanza/models"
	"github.com/ayush00git/stanza/services"
)

var dockingJobs = services.NewJobStore()

type dockPOSTBody struct {
	PocketID       int    `json:"pocket_id"`
	SourceType     string `json:"source_type"`
	LigandSMILES   string `json:"ligand_smiles"`
	ProteinPDBPath string `json:"protein_pdb_path"`
	ProteinPDBID   string `json:"protein_pdb_id"`
}

// DockSubmitHandler handles POST /dock — validates the request, enqueues a job, and responds HTTP 202.
func DockSubmitHandler(c *gin.Context) {
	if ct := c.GetHeader("Content-Type"); !strings.Contains(strings.ToLower(ct), "application/json") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Content-Type must be application/json"})
		return
	}

	var body dockPOSTBody
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return
	}

	proteinPath := strings.TrimSpace(body.ProteinPDBPath)
	if proteinPath == "" {
		proteinPath = strings.TrimSpace(body.ProteinPDBID)
	}
	if body.PocketID <= 0 || strings.TrimSpace(body.LigandSMILES) == "" || proteinPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pocket_id, ligand_smiles, and protein_pdb_path (or protein_pdb_id) are required"})
		return
	}

	sourceType := strings.TrimSpace(body.SourceType)
	if sourceType == "" {
		sourceType = "dimer"
	}

	pocket, ok := DefaultPocketStore.Get(sourceType, body.PocketID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("pocket %s:%d not found", sourceType, body.PocketID)})
		return
	}

	lig := models.Fragment{SMILES: strings.TrimSpace(body.LigandSMILES)}
	jobID := dockingJobs.Submit(pocket, lig, proteinPath)

	c.JSON(http.StatusAccepted, gin.H{"job_id": jobID})
}

// DockStatusHandler handles GET /dock/status?id=<jobID>.
func DockStatusHandler(c *gin.Context) {
	id := c.Query("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'id' is required"})
		return
	}

	res, ok := dockingJobs.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	c.JSON(http.StatusOK, res)
}
