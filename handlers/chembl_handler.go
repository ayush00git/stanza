package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ayush00git/stanza/models"
	"github.com/ayush00git/stanza/services"
)

// ChemblHandler handles GET /chembl?pocket_id=<int> and optional volume, hydrophobicity, polarity query params
// (from the binding-site table row) to drive ChEMBL fragment selection.
func ChemblHandler(c *gin.Context) {
	idStr := c.Query("pocket_id")
	if idStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'pocket_id' is required"})
		return
	}
	pid, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'pocket_id' must be an integer"})
		return
	}

	sourceType := c.Query("source_type")
	if sourceType == "" {
		sourceType = "dimer"
	}

	pocket, ok := DefaultPocketStore.Get(sourceType, pid)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("pocket %s:%d not found", sourceType, pid)})
		return
	}

	pocket = applyPocketQueryOverrides(pocket, c.Query("volume"), c.Query("hydrophobicity"), c.Query("polarity"))
	c.JSON(http.StatusOK, services.FetchFragments(pocket))
}

func applyPocketQueryOverrides(p models.Pocket, volStr, hydroStr, polStr string) models.Pocket {
	if v, ok := parseQueryFloat(volStr); ok {
		p.Volume = v
	}
	if h, ok := parseQueryFloat(hydroStr); ok {
		p.Hydrophobicity = h
	}
	if pol, ok := parseQueryFloat(polStr); ok {
		p.Polarity = pol
	}
	return p
}

func parseQueryFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
