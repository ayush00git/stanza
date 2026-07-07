package handlers

import (
	"fmt"
	"sync"

	"github.com/ayush00git/stanza/models"
)

// PocketStore holds pockets keyed by composite "sourceType:pocketID" so that
// monomer and dimer pockets with the same fpocket numeric ID never collide.
type PocketStore struct {
	mu   sync.RWMutex
	byID map[string]models.Pocket
}

// DefaultPocketStore is the process-wide pocket registry populated by binding-sites responses.
var DefaultPocketStore = NewPocketStore()

// NewPocketStore creates an empty PocketStore.
func NewPocketStore() *PocketStore {
	return &PocketStore{byID: make(map[string]models.Pocket)}
}

// pocketKey builds the composite key "monomer:3" / "dimer:1".
func pocketKey(sourceType string, pocketID int) string {
	return fmt.Sprintf("%s:%d", sourceType, pocketID)
}

// Put stores or replaces a pocket.
func (s *PocketStore) Put(p models.Pocket) {
	s.mu.Lock()
	s.byID[pocketKey(p.SourceType, p.PocketID)] = p
	s.mu.Unlock()
}

// RegisterBindingSitesResult indexes all pockets from a binding-sites run.
func (s *PocketStore) RegisterBindingSitesResult(pockets, monomerPockets []models.Pocket) {
	for _, p := range pockets {
		s.Put(p)
	}
	for _, p := range monomerPockets {
		s.Put(p)
	}
}

// Get returns a pocket by source type and fpocket numeric ID.
func (s *PocketStore) Get(sourceType string, pocketID int) (models.Pocket, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[pocketKey(sourceType, pocketID)]
	return p, ok
}
