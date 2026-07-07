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

	r.Run(":8080")
}
