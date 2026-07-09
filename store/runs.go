package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ayush00git/stanza/models"
)

// runColumns is the ordered runs-row projection scanned by scanRunHeader.
const runColumns = `id, profile_id, uniprot_id, mutation, mutation_wt, mutation_pos, ` +
	`mutation_mut, site_hint, status, error, wt_structure, mutagenesis, pockets, created_at`

// smilesHash returns the hex-encoded sha256 of a SMILES string, matching the
// smiles_hash column used to dedupe chemistries within a run.
func smilesHash(smiles string) string {
	sum := sha256.Sum256([]byte(smiles))
	return hex.EncodeToString(sum[:])
}

// jsonbOrNil marshals a non-nil value to JSON bytes for a jsonb column, or
// returns a nil interface so the column is written as SQL NULL (not "null").
func jsonbOrNil(v any) (any, error) {
	// v is an interface holding a possibly-nil typed pointer; the caller passes
	// nil directly for the nil case, so a non-nil v here is always marshalable.
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// SaveRun upserts the whole run aggregate (header + molecules + docks) in one
// transaction. Children are replaced wholesale, which is the simplest correct
// approach for full-aggregate saves.
func (s *Store) SaveRun(ctx context.Context, run *models.Run) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	// profile_id: "" → SQL NULL to avoid violating the FK for anonymous runs.
	var profileID any
	if run.ProfileID != "" {
		profileID = run.ProfileID
	}

	var wtJSON, mutaJSON, pocketsJSON any
	if run.WTStructure != nil {
		if wtJSON, err = jsonbOrNil(run.WTStructure); err != nil {
			return err
		}
	}
	if run.Mutagenesis != nil {
		if mutaJSON, err = jsonbOrNil(run.Mutagenesis); err != nil {
			return err
		}
	}
	if run.Pockets != nil {
		if pocketsJSON, err = jsonbOrNil(run.Pockets); err != nil {
			return err
		}
	}

	createdAt, err := time.Parse(time.RFC3339, run.CreatedAt)
	if err != nil {
		createdAt = time.Now().UTC()
	}

	const upsertRun = `
INSERT INTO runs (id, profile_id, uniprot_id, mutation, mutation_wt, mutation_pos,
                  mutation_mut, site_hint, status, error, wt_structure, mutagenesis,
                  pockets, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (id) DO UPDATE SET
    profile_id   = EXCLUDED.profile_id,
    uniprot_id   = EXCLUDED.uniprot_id,
    mutation     = EXCLUDED.mutation,
    mutation_wt  = EXCLUDED.mutation_wt,
    mutation_pos = EXCLUDED.mutation_pos,
    mutation_mut = EXCLUDED.mutation_mut,
    site_hint    = EXCLUDED.site_hint,
    status       = EXCLUDED.status,
    error        = EXCLUDED.error,
    wt_structure = EXCLUDED.wt_structure,
    mutagenesis  = EXCLUDED.mutagenesis,
    pockets      = EXCLUDED.pockets,
    created_at   = EXCLUDED.created_at,
    updated_at   = now()`

	if _, err = tx.Exec(ctx, upsertRun,
		run.ID, profileID, run.UniprotID, run.Mutation.Raw, run.Mutation.WildType,
		run.Mutation.Position, run.Mutation.Mutant, run.SiteHint, run.Status, run.Error,
		wtJSON, mutaJSON, pocketsJSON, createdAt,
	); err != nil {
		return err
	}

	// Replace molecules.
	if _, err = tx.Exec(ctx, `DELETE FROM molecules WHERE run_id = $1`, run.ID); err != nil {
		return err
	}
	const insertMolecule = `
INSERT INTO molecules (id, run_id, round, smiles, smiles_hash, inchikey, qed,
                       ro5_pass, sa_score, mol_weight, logp)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (run_id, smiles_hash) DO NOTHING`
	for _, c := range run.Candidates {
		if _, err = tx.Exec(ctx, insertMolecule,
			uuid.NewString(), run.ID, 0, c.SMILES, smilesHash(c.SMILES), c.InChIKey,
			c.QED, c.RO5Pass, c.SAScore, c.MolWeight, c.LogP,
		); err != nil {
			return err
		}
	}

	// Replace docks.
	if _, err = tx.Exec(ctx, `DELETE FROM docks WHERE run_id = $1`, run.ID); err != nil {
		return err
	}
	const insertDock = `
INSERT INTO docks (id, run_id, smiles, smiles_hash, wt_score, mutant_score,
                   selectivity, wt_pose_pdb, mutant_pose_pdb)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (run_id, smiles_hash) DO NOTHING`
	for _, d := range run.Docks {
		if _, err = tx.Exec(ctx, insertDock,
			uuid.NewString(), run.ID, d.SMILES, smilesHash(d.SMILES), d.WTScore,
			d.MutantScore, d.Selectivity, d.WTPosePDB, d.MutantPosePDB,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// scanRunHeader rebuilds a run header (no children) from a runs-row scan. The
// row must project runColumns in order.
func scanRunHeader(row pgx.Row) (*models.Run, error) {
	var (
		r           models.Run
		profileID   *string
		mutation    string
		mutWT       string
		mutPos      int
		mutMut      string
		wtJSON      []byte
		mutaJSON    []byte
		pocketsJSON []byte
		createdAt   time.Time
	)
	if err := row.Scan(
		&r.ID, &profileID, &r.UniprotID, &mutation, &mutWT, &mutPos, &mutMut,
		&r.SiteHint, &r.Status, &r.Error, &wtJSON, &mutaJSON, &pocketsJSON, &createdAt,
	); err != nil {
		return nil, err
	}

	if profileID != nil {
		r.ProfileID = *profileID
	}
	r.Mutation = models.Mutation{Raw: mutation, WildType: mutWT, Position: mutPos, Mutant: mutMut}
	r.CreatedAt = createdAt.UTC().Format(time.RFC3339)

	if len(wtJSON) > 0 {
		var wt models.WTStructure
		if err := json.Unmarshal(wtJSON, &wt); err != nil {
			return nil, err
		}
		r.WTStructure = &wt
	}
	if len(mutaJSON) > 0 {
		var muta models.MutagenesisResult
		if err := json.Unmarshal(mutaJSON, &muta); err != nil {
			return nil, err
		}
		r.Mutagenesis = &muta
	}
	if len(pocketsJSON) > 0 {
		var pockets models.PocketAnalysis
		if err := json.Unmarshal(pocketsJSON, &pockets); err != nil {
			return nil, err
		}
		r.Pockets = &pockets
	}

	return &r, nil
}

// loadCandidates reads a run's molecules (Stage-6 candidates) oldest-first.
func (s *Store) loadCandidates(ctx context.Context, runID string) ([]models.Candidate, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT smiles, inchikey, qed, ro5_pass, sa_score, mol_weight, logp
FROM molecules WHERE run_id = $1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Candidate
	for rows.Next() {
		var c models.Candidate
		if err := rows.Scan(&c.SMILES, &c.InChIKey, &c.QED, &c.RO5Pass, &c.SAScore, &c.MolWeight, &c.LogP); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// loadDocks reads a run's paired docks oldest-first.
func (s *Store) loadDocks(ctx context.Context, runID string) ([]models.LigandDock, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT smiles, wt_score, mutant_score, selectivity, wt_pose_pdb, mutant_pose_pdb
FROM docks WHERE run_id = $1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.LigandDock
	for rows.Next() {
		var d models.LigandDock
		if err := rows.Scan(&d.SMILES, &d.WTScore, &d.MutantScore, &d.Selectivity, &d.WTPosePDB, &d.MutantPosePDB); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetRun returns the full run aggregate. The bool is false when no run has the
// given id.
func (s *Store) GetRun(ctx context.Context, id string) (*models.Run, bool, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, id)
	run, err := scanRunHeader(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}

	if run.Candidates, err = s.loadCandidates(ctx, id); err != nil {
		return nil, false, err
	}
	if run.Docks, err = s.loadDocks(ctx, id); err != nil {
		return nil, false, err
	}
	return run, true, nil
}

// ListRuns returns run aggregates newest-first. profileID=="" lists every run;
// otherwise only that profile's runs.
func (s *Store) ListRuns(ctx context.Context, profileID string) ([]*models.Run, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if profileID == "" {
		rows, err = s.Pool.Query(ctx, `SELECT `+runColumns+` FROM runs ORDER BY created_at DESC`)
	} else {
		rows, err = s.Pool.Query(ctx, `SELECT `+runColumns+` FROM runs WHERE profile_id = $1 ORDER BY created_at DESC`, profileID)
	}
	if err != nil {
		return nil, err
	}

	var runs []*models.Run
	for rows.Next() {
		run, err := scanRunHeader(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, run)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load children per-run. A simple loop is fine at this scale.
	for _, run := range runs {
		if run.Candidates, err = s.loadCandidates(ctx, run.ID); err != nil {
			return nil, err
		}
		if run.Docks, err = s.loadDocks(ctx, run.ID); err != nil {
			return nil, err
		}
	}
	return runs, nil
}
