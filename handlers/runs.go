package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
		DefaultRunStore.Put(run)
		c.JSON(http.StatusCreated, run)
		return
	}
	run.Status = "structure_acquired"
	run.WTStructure = result

	// Stage-2 mutagenesis: build the matched WT/mutant structure pair. A failure
	// here is recorded on the (successful Stage-1) run rather than dropping it.
	mut, merr := services.BuildMutagenesis(c.Request.Context(), run.ID, uniprotID, mutation)
	if merr != nil {
		run.WTStructure.Notes = append(run.WTStructure.Notes, "mutagenesis failed: "+merr.Error())
	} else {
		run.Mutagenesis = mut
		run.Status = "mutant_built"
	}

	DefaultRunStore.Put(run)
	c.JSON(http.StatusCreated, run)
}

// ServeRunStructureHandler handles GET /runs/:id/structure/:track, returning the
// Stage-2 generated PDB for a run's "wt" or "mutant" track.
func ServeRunStructureHandler(c *gin.Context) {
	id := c.Param("id")
	track := c.Param("track")
	if track != "wt" && track != "mutant" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "track must be 'wt' or 'mutant'"})
		return
	}
	if _, ok := DefaultRunStore.Get(id); !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	path := services.RunStructurePath(id, track)
	if _, err := os.Stat(path); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "structure not available for this run"})
		return
	}
	c.Header("Content-Type", "chemical/x-pdb")
	c.File(path)
}

// GetRunPocketsHandler handles GET /runs/:id/pockets — Stage 3. It runs fpocket on
// the run's WT and mutant structures, computes the WT→mutant pocket delta, caches
// the result on the run, and returns it. Requires Stage-2 mutagenesis to have run.
func GetRunPocketsHandler(c *gin.Context) {
	id := c.Param("id")
	run, ok := DefaultRunStore.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	if run.Pockets != nil {
		c.JSON(http.StatusOK, run.Pockets)
		return
	}
	if run.Mutagenesis == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "run has no mutant structure yet"})
		return
	}

	pa, err := services.BuildPocketAnalysis(c.Request.Context(), run)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	run.Pockets = pa
	DefaultRunStore.Put(run)
	c.JSON(http.StatusOK, pa)
}

type dockRunBody struct {
	LigandSMILES string `json:"ligand_smiles"`
}

// DockRunHandler handles POST /runs/:id/dock — Stage 4. It docks a SMILES ligand
// into the run's WT and mutant resistance pockets and returns the paired scores +
// selectivity. Runs Stage-3 pocket analysis first if it hasn't been done, and
// caches per-SMILES so re-docking the same molecule is free.
func DockRunHandler(c *gin.Context) {
	id := c.Param("id")
	run, ok := DefaultRunStore.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}

	if ct := c.GetHeader("Content-Type"); !strings.Contains(strings.ToLower(ct), "application/json") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Content-Type must be application/json"})
		return
	}
	var body dockRunBody
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return
	}
	smiles := strings.TrimSpace(body.LigandSMILES)
	if smiles == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ligand_smiles is required"})
		return
	}

	// Cache: the same molecule re-docked against this run is returned as-is.
	for i := range run.Docks {
		if run.Docks[i].SMILES == smiles {
			c.JSON(http.StatusOK, run.Docks[i])
			return
		}
	}

	if run.Mutagenesis == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "run has no mutant structure yet"})
		return
	}
	// Ensure Stage-3 pocket analysis has run (docking needs the pocket box).
	if run.Pockets == nil {
		pa, err := services.BuildPocketAnalysis(c.Request.Context(), run)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("pocket analysis: %v", err)})
			return
		}
		run.Pockets = pa
	}

	res, err := services.DockLigandDualTrack(c.Request.Context(), run, smiles)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	run.Docks = append(run.Docks, *res)
	DefaultRunStore.Put(run)
	c.JSON(http.StatusCreated, res)
}

// ListRunDocksHandler handles GET /runs/:id/docks, returning the run's docked
// molecules (the selectivity leaderboard).
func ListRunDocksHandler(c *gin.Context) {
	id := c.Param("id")
	run, ok := DefaultRunStore.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	docks := run.Docks
	if docks == nil {
		docks = []models.LigandDock{}
	}
	c.JSON(http.StatusOK, gin.H{"docks": docks})
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
