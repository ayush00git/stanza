package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/ayush00git/stanza/models"
	"github.com/ayush00git/stanza/store"
)

// DefaultStore is the process-wide Postgres store for durable run/profile
// persistence. It is nil when no database is configured (DATABASE_URL unset or
// the connection failed), in which case the app runs with in-memory runs only
// and profile endpoints degrade gracefully.
var DefaultStore *store.Store

// persistRun best-effort writes a run to the durable store. It is a no-op when no
// database is configured, and never fails the request: a persistence error is
// logged and swallowed. The run is snapshotted under genMu so the background
// generation loop isn't mutating it mid-write.
func persistRun(ctx context.Context, run *models.Run) {
	if DefaultStore == nil {
		return
	}
	genMu.Lock()
	snap := *run
	genMu.Unlock()
	if err := DefaultStore.SaveRun(ctx, &snap); err != nil {
		log.Printf("[runs] persist %s: %v", run.ID, err)
	}
}

// createProfileBody is the POST /profiles request payload.
type createProfileBody struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	Institution string `json:"institution"`
	Field       string `json:"field"`
	ORCID       string `json:"orcid"`
}

// CreateProfileHandler handles POST /profiles. It creates a researcher profile
// from a short form and responds 201 with it. Requires the database.
func CreateProfileHandler(c *gin.Context) {
	if ct := c.GetHeader("Content-Type"); !strings.Contains(strings.ToLower(ct), "application/json") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Content-Type must be application/json"})
		return
	}

	var body createProfileBody
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	// Profiles are the durable-only feature: without a database there is nowhere
	// to anchor run history, so fail clearly rather than pretend to succeed.
	if DefaultStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "profiles require the database (set DATABASE_URL)"})
		return
	}

	p := &models.Profile{
		ID:          uuid.NewString(),
		Name:        name,
		Email:       strings.TrimSpace(body.Email),
		Institution: strings.TrimSpace(body.Institution),
		Field:       strings.TrimSpace(body.Field),
		ORCID:       strings.TrimSpace(body.ORCID),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := DefaultStore.CreateProfile(c.Request.Context(), p); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, p)
}

// ListProfilesHandler handles GET /profiles, returning all profiles. With no
// database configured it returns an empty list rather than an error.
func ListProfilesHandler(c *gin.Context) {
	if DefaultStore == nil {
		c.JSON(http.StatusOK, gin.H{"profiles": []*models.Profile{}})
		return
	}

	profiles, err := DefaultStore.ListProfiles(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if profiles == nil {
		profiles = []*models.Profile{}
	}
	c.JSON(http.StatusOK, gin.H{"profiles": profiles})
}

// GetProfileHandler handles GET /profiles/:id.
func GetProfileHandler(c *gin.Context) {
	if DefaultStore == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
		return
	}

	id := c.Param("id")
	p, ok, err := DefaultStore.GetProfile(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
		return
	}
	c.JSON(http.StatusOK, p)
}
