package handlers

import (
	"sync"

	"github.com/ayush00git/stanza/models"
)

// maxRuns caps the in-memory run registry; the oldest run is evicted past this,
// mirroring the docking JobStore and PocketStore bounds.
const maxRuns = 200

// RunStore keeps created runs in memory (Stage 1; durable persistence is a later
// stage). It is safe for concurrent use.
type RunStore struct {
	mu    sync.RWMutex
	byID  map[string]*models.Run
	order []string // insertion order, oldest first — for eviction and newest-first listing
}

// DefaultRunStore is the process-wide run registry populated by the /runs routes.
var DefaultRunStore = NewRunStore()

// NewRunStore creates an empty RunStore.
func NewRunStore() *RunStore {
	return &RunStore{
		byID:  make(map[string]*models.Run),
		order: make([]string, 0, 16),
	}
}

// Put stores or replaces a run, evicting the oldest entries past maxRuns.
func (s *RunStore) Put(r *models.Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[r.ID]; !exists {
		s.order = append(s.order, r.ID)
	}
	s.byID[r.ID] = r
	s.evictIfNeededLocked()
}

// Get returns a run by ID.
func (s *RunStore) Get(id string) (*models.Run, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byID[id]
	return r, ok
}

// List returns all runs, newest-first.
func (s *RunStore) List() []*models.Run {
	s.mu.RLock()
	defer s.mu.RUnlock()
	runs := make([]*models.Run, 0, len(s.order))
	for i := len(s.order) - 1; i >= 0; i-- {
		if r, ok := s.byID[s.order[i]]; ok {
			runs = append(runs, r)
		}
	}
	return runs
}

// evictIfNeededLocked drops the oldest runs until the store is within maxRuns.
// The caller must hold s.mu.
func (s *RunStore) evictIfNeededLocked() {
	for len(s.order) > maxRuns {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.byID, oldest)
	}
}
