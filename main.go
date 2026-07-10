package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/ayush00git/stanza/handlers"
	"github.com/ayush00git/stanza/store"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env (e.g. ANTHROPIC_API_KEY for the generation loop) if present;
	// real environment variables still take precedence.
	_ = godotenv.Load()

	// Optional Postgres persistence (feature 08). The app degrades gracefully:
	// without DATABASE_URL — or if the connection fails — it runs with in-memory
	// runs only and profile endpoints report that the database is unavailable.
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Println("[store] DATABASE_URL not set — running with in-memory runs only (no persistence)")
	} else if st, err := store.New(ctx, dbURL); err != nil {
		log.Printf("[store] disabled (%v) — running in-memory only", err)
	} else {
		if err := st.Migrate(ctx); err != nil {
			log.Printf("[store] migrate failed: %v", err)
		}
		// Hydrate the in-memory cache so history loads on boot. ListRuns is
		// newest-first, so Put in reverse (oldest first) to keep List() newest-first.
		if runs, err := st.ListRuns(ctx, ""); err != nil {
			log.Printf("[store] hydrate failed: %v", err)
		} else {
			for i := len(runs) - 1; i >= 0; i-- {
				handlers.DefaultRunStore.Put(runs[i])
			}
			log.Printf("[store] hydrated %d run(s) from the database", len(runs))
		}
		handlers.DefaultStore = st
		defer st.Close()
	}

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
	// Stage-2 generated structures (matched WT/mutant pair) for a run.
	r.GET("/runs/:id/structure/:track", handlers.ServeRunStructureHandler)
	// Stage-3 WT/mutant pocket analysis + delta for a run.
	r.GET("/runs/:id/pockets", handlers.GetRunPocketsHandler)
	// Stage-4 dual-track docking (WT + mutant) for a run.
	r.POST("/runs/:id/dock", handlers.DockRunHandler)
	r.GET("/runs/:id/dock/stream", handlers.DockRunStreamHandler)
	r.GET("/runs/:id/docks", handlers.ListRunDocksHandler)
	// Stage-7 selectivity scoring + ranking: the docked molecules as a fitness leaderboard.
	r.GET("/runs/:id/ranking", handlers.GetRunRankingHandler)
	// Stage-6 Claude molecule generation (propose + RDKit filter) for a run.
	r.POST("/runs/:id/generate", handlers.GenerateRunHandler)
	r.GET("/runs/:id/generate/stream", handlers.GenerateRunStreamHandler)

	// Researcher profiles (Stage 8): create, list, and fetch the identities that
	// own run history. These require Postgres; without it they degrade gracefully.
	r.POST("/profiles", handlers.CreateProfileHandler)
	r.GET("/profiles", handlers.ListProfilesHandler)
	r.GET("/profiles/:id", handlers.GetProfileHandler)

	r.Run(":8080")
}
