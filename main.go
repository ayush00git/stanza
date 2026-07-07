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

	// Protein data routes (AlphaFold + UniProt), keyed by UniProt accession.
	protein := r.Group("/protein")
	{
		protein.GET("/:id", handlers.GetProtein)         // combined UniProt + AlphaFold
		protein.GET("/:id/monomer", handlers.GetMonomer) // AlphaFold monomer prediction
		protein.GET("/:id/dimer", handlers.GetDimer)     // AlphaFold complex (dimer) data
	}

	r.Run(":8080")
}
