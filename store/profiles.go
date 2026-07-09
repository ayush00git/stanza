package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ayush00git/stanza/models"
)

// CreateProfile inserts a researcher profile, filling p.ID (a new uuid) and
// p.CreatedAt (RFC3339, UTC) when empty.
func (s *Store) CreateProfile(ctx context.Context, p *models.Profile) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.CreatedAt == "" {
		p.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	createdAt, err := time.Parse(time.RFC3339, p.CreatedAt)
	if err != nil {
		createdAt = time.Now().UTC()
		p.CreatedAt = createdAt.Format(time.RFC3339)
	}

	_, err = s.Pool.Exec(ctx, `
INSERT INTO profiles (id, name, email, institution, field, orcid, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		p.ID, p.Name, p.Email, p.Institution, p.Field, p.ORCID, createdAt)
	return err
}

// GetProfile returns a profile by id. The bool is false when not found.
func (s *Store) GetProfile(ctx context.Context, id string) (*models.Profile, bool, error) {
	row := s.Pool.QueryRow(ctx, `
SELECT id, name, email, institution, field, orcid, created_at
FROM profiles WHERE id = $1`, id)

	var (
		p         models.Profile
		createdAt time.Time
	)
	if err := row.Scan(&p.ID, &p.Name, &p.Email, &p.Institution, &p.Field, &p.ORCID, &createdAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	p.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return &p, true, nil
}

// ListProfiles returns all profiles newest-first.
func (s *Store) ListProfiles(ctx context.Context) ([]*models.Profile, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT id, name, email, institution, field, orcid, created_at
FROM profiles ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*models.Profile
	for rows.Next() {
		var (
			p         models.Profile
			createdAt time.Time
		)
		if err := rows.Scan(&p.ID, &p.Name, &p.Email, &p.Institution, &p.Field, &p.ORCID, &createdAt); err != nil {
			return nil, err
		}
		p.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		out = append(out, &p)
	}
	return out, rows.Err()
}
