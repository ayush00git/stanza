package main

import (
	"net/http"

	"github.com/ayush00git/stanza/handlers"
	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	// Search (SSE) and per-complex detail: monomer + dimer data in one response.
	r.GET("/search", handlers.SearchHandler)
	r.GET("/complex/:id", handlers.ComplexDetailHandler)

	// fpocket binding-site analysis: runs fpocket on the monomer and dimer
	// structures and returns detected pockets with a monomer/dimer comparison.
	r.GET("/complex/:id/binding-sites", handlers.BindingSiteHandler)

	// ChEMBL drug coverage, fetched lazily so it never blocks the structure
	// viewers on the detail page.
	r.GET("/complex/:id/drugs", handlers.ComplexDrugsHandler)

	// ChEMBL fragment selection for a pocket.
	r.GET("/chembl", handlers.ChemblHandler)

	// Docking: submit an async job and poll its status.
	r.POST("/dock", handlers.DockSubmitHandler)
	r.GET("/dock/status", handlers.DockStatusHandler)

	// Resistance-design runs (Stage 1): POST acquires the wild-type structure via
	// the experimental → AlphaFold ladder plus residue verification; GET fetches
	// a single run or lists all.
	r.POST("/runs", handlers.CreateRunHandler)
	r.GET("/runs/:id", handlers.GetRunHandler)
	r.GET("/runs", handlers.ListRunsHandler)

	r.Run(":8080")
}
