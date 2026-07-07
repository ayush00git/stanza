package services

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ayush00git/stanza/models"
	"github.com/google/uuid"
)

const maxJobs = 100

// DockingResult is the status and output of a docking job.
type DockingResult struct {
	JobID           string         `json:"job_id"`
	PocketID        int            `json:"pocket_id"`
	Status          string         `json:"status"` // pending, running, done, error
	BindingAffinity float64        `json:"binding_affinity"`
	PosePDB         string         `json:"pose_pdb"` // Mol* compatible PDB content
	Error           string         `json:"error,omitempty"`
	Conformations   []Conformation `json:"conformations,omitempty"`
}

type Conformation struct {
	Mode            int     `json:"mode"`
	BindingAffinity float64 `json:"binding_affinity"`
	RMSDLB          float64 `json:"rmsd_lb"`
	RMSDUB          float64 `json:"rmsd_ub"`
	PosePDB         string  `json:"pose_pdb"`
}

// JobStore tracks asynchronous docking jobs in memory.
type JobStore struct {
	mu      sync.RWMutex
	order   []string
	results map[string]DockingResult
}

// NewJobStore creates an empty JobStore.
func NewJobStore() *JobStore {
	return &JobStore{
		results: make(map[string]DockingResult),
		order:   make([]string, 0, 16),
	}
}

// Submit queues a docking run and returns immediately with a job ID.
func (s *JobStore) Submit(pocket models.Pocket, ligand models.Fragment, proteinPDBPath string) string {
	jobID := uuid.NewString()

	s.mu.Lock()
	s.evictIfNeededLocked(maxJobs - 1)
	s.order = append(s.order, jobID)
	s.results[jobID] = DockingResult{
		JobID:    jobID,
		PocketID: pocket.PocketID,
		Status:   "pending",
	}
	s.mu.Unlock()

	go s.runJob(context.Background(), jobID, pocket, ligand, proteinPDBPath)
	return jobID
}

// runJob orchestrates the docking pipeline steps defined in docking.go.
func (s *JobStore) runJob(ctx context.Context, jobID string, pocket models.Pocket, ligand models.Fragment, proteinPDBPath string) {
	// 1. Mark as running
	s.updateStatus(jobID, "running", "", 0, "")

	// 2. Create workspace
	tmpDir, err := os.MkdirTemp("", "docking-"+jobID)
	if err != nil {
		s.updateError(jobID, fmt.Errorf("failed to create workspace: %w", err))
		return
	}
	defer os.RemoveAll(tmpDir)

	// 2b. Download protein if URL
	localProteinPath := proteinPDBPath
	if isURL(proteinPDBPath) {
		localProteinPath = filepath.Join(tmpDir, "receptor.pdb")
		if err := downloadFile(ctx, proteinPDBPath, localProteinPath); err != nil {
			s.updateError(jobID, fmt.Errorf("failed to download protein for docking: %w", err))
			return
		}
	}

	// 3. Prepare ligand 3D from SMILES
	log.Printf("[dock:%s] SMILES input: %s", jobID[:8], ligand.SMILES)
	ligPDB, err := SMILESTo3D(ligand.SMILES, tmpDir)
	if err != nil {
		s.updateError(jobID, fmt.Errorf("ligand 3D generation failed: %w", err))
		return
	}
	log.Printf("[dock:%s] ligand 3D PDB: %s", jobID[:8], ligPDB)
	logFileHead("ligand PDB", ligPDB, jobID)

	// 4. Prepare files for Vina (PDBQT conversion)
	receptorPDBQT, err := PrepareReceptor(localProteinPath, tmpDir)
	if err != nil {
		s.updateError(jobID, fmt.Errorf("receptor prep failed: %w", err))
		return
	}
	logFileHead("receptor PDBQT (after strip)", receptorPDBQT, jobID)

	ligandPDBQT, err := PrepareLigand(ligPDB, tmpDir)
	if err != nil {
		s.updateError(jobID, fmt.Errorf("ligand prep failed: %w", err))
		return
	}
	logFileHead("ligand PDBQT", ligandPDBQT, jobID)

	// 5. Run Vina
	log.Printf("[dock:%s] Running Vina: receptor=%s ligand=%s center=%.1f,%.1f,%.1f",
		jobID[:8], receptorPDBQT, ligandPDBQT,
		pocket.Center[0], pocket.Center[1], pocket.Center[2])
	res, err := RunVinaDock(receptorPDBQT, ligandPDBQT, pocket, tmpDir)
	if err != nil {
		s.updateError(jobID, fmt.Errorf("docking run failed: %w", err))
		return
	}
	log.Printf("[dock:%s] Vina complete: affinity=%.2f", jobID[:8], res.BindingAffinity)

	// 6. Read final pose PDB to return to client
	poseContent, err := os.ReadFile(res.DockedPDB)
	if err != nil {
		// Fallback: if PDB missing, use PDBQT content if exists
		if content, err2 := os.ReadFile(res.DockedPDBQT); err2 == nil {
			poseContent = content
		} else {
			s.updateError(jobID, fmt.Errorf("failed to read docking results: %w", err))
			return
		}
	}

	// 7. Update store with final result
	s.mu.Lock()
	entry := s.results[jobID]
	entry.Status = "done"
	entry.BindingAffinity = res.BindingAffinity
	entry.PosePDB = string(poseContent)
	s.results[jobID] = entry
	s.mu.Unlock()
}

func (s *JobStore) updateStatus(jobID, status, errStr string, affinity float64, pdb string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.results[jobID]; ok {
		entry.Status = status
		entry.Error = errStr
		entry.BindingAffinity = affinity
		entry.PosePDB = pdb
		s.results[jobID] = entry
	}
}

func (s *JobStore) updateError(jobID string, err error) {
	s.updateStatus(jobID, "error", err.Error(), 0, "")
}

// Get returns the status of a job.
func (s *JobStore) Get(jobID string) (DockingResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.results[jobID]
	return r, ok
}

func (s *JobStore) evictIfNeededLocked(maxLen int) {
	for len(s.order) > maxLen {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.results, old)
	}
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// logFileHead prints the first N lines of a file for debugging docking PDBQT issues.
func logFileHead(label, path, jobID string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[dock:%s] %s: read error: %v", jobID[:8], label, err)
		return
	}
	lines := strings.SplitN(string(data), "\n", 16) // first 15 lines + remainder
	if len(lines) > 15 {
		lines = lines[:15]
	}
	log.Printf("[dock:%s] === %s (%d bytes, showing first %d lines) ===", jobID[:8], label, len(data), len(lines))
	for i, l := range lines {
		log.Printf("[dock:%s]   %d: %s", jobID[:8], i+1, l)
	}
}
