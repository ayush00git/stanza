package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ComplexDetailHandler handles GET /complex/:id
// :id can be either a UniProt ID (e.g. P04637) or an AlphaFold ID (e.g. AF-P04637-F1).
// It fetches UniProt metadata plus the AlphaFold monomer and dimer predictions.
func ComplexDetailHandler(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path parameter 'id' is required"})
		return
	}

	uniprotID := normalizeToUniProtID(id)

	complex, err := buildComplexFromUniProt(uniprotID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("live fetch failed for %s: %v", uniprotID, err)})
		return
	}
	c.JSON(http.StatusOK, complex)
}

// normalizeToUniProtID extracts the UniProt accession from an AlphaFold ID.
// "AF-P04637-F1" → "P04637"
// "P04637" → "P04637" (unchanged)
func normalizeToUniProtID(id string) string {
	if len(id) > 3 && id[:3] == "AF-" {
		parts := strings.Split(id, "-")
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	return id
}
