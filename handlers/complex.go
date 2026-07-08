package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ayush00git/stanza/services"
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

// ComplexDrugsHandler handles GET /complex/:id/drugs.
// It fetches ChEMBL drug coverage (drug count + known drug names) for a target
// on demand. This is split out from ComplexDetailHandler because the ChEMBL
// lookup is slow — it paginates through every activity page for the target — and
// would otherwise block the structure viewers on the detail page.
func ComplexDrugsHandler(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path parameter 'id' is required"})
		return
	}

	uniprotID := normalizeToUniProtID(id)

	count, names, _ := services.FetchDrugCoverage(uniprotID)
	if names == nil {
		names = []string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"drug_count":       count,
		"known_drug_names": names,
	})
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
