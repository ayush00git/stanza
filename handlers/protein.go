package handlers

import (
	"net/http"

	"github.com/ayush00git/stanza/services"
	"github.com/gin-gonic/gin"
)

// GetMonomer returns the AlphaFold monomer prediction for a UniProt ID as JSON.
// GET /protein/:id/monomer
func GetMonomer(c *gin.Context) {
	id := c.Param("id")
	pred, err := services.FetchMonomerPrediction(id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, pred)
}

// GetDimer returns the AlphaFold complex (dimer) vs monomer comparison as JSON.
// GET /protein/:id/dimer
func GetDimer(c *gin.Context) {
	id := c.Param("id")
	data, err := services.FetchComplexData(id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

// GetProtein returns a combined view: UniProt metadata (including subunit
// structure) plus the AlphaFold monomer and dimer data, all as one JSON object.
// GET /protein/:id
func GetProtein(c *gin.Context) {
	id := c.Param("id")

	entry, err := services.FetchUniProtEntry(id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	// AlphaFold data is best-effort: a protein can exist in UniProt without a
	// prediction, so a missing monomer/dimer degrades to null rather than 502.
	monomer, _ := services.FetchMonomerPrediction(id)
	dimer, _ := services.FetchComplexData(id)

	c.JSON(http.StatusOK, gin.H{
		"accession":        entry.PrimaryAccession,
		"entryType":        entry.EntryType,
		"name":             entry.ProteinDescription.RecommendedName.FullName.Value,
		"organism":         entry.Organism.ScientificName,
		"taxonId":          entry.Organism.TaxonID,
		"length":           entry.Sequence.Length,
		"subunitStructure": entry.SubunitStructure(),
		"monomer":          monomer,
		"dimer":            dimer,
	})
}
