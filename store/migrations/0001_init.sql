-- Stanza persistence — Stage 8 (persistence + profiles).
--
-- The Postgres system of record for researcher profiles and resistance-design
-- runs, replacing the process-local in-memory stores so run history survives a
-- restart. Applied idempotently at startup by store.Migrate (every statement is
-- IF NOT EXISTS), so it is safe to re-run.
--
-- Scope note: this is the "persistence + profiles" half of feature 08. Variable
-- shape stage outputs (Stage-1 acquisition metadata, the Stage-2 built pair, and
-- the Stage-3 pocket analysis) are stored as jsonb on the run — the same choice
-- the 08 spec makes for pocket key_residues/delta — while the growing list
-- entities (molecules, docks) are normalized into their own tables. The full
-- structures/pockets/scores normalization, the dock cache-key UNIQUE index, and
-- the Redis queue + Python worker pool remain deferred to the queue build, where
-- those tables are actually queried. See docs/features/08-persistence-and-queue.md.
--
-- IDs are TEXT holding UUID strings generated app-side (google/uuid), avoiding
-- pgx uuid-type juggling for values Go already produces as strings.

-- profiles: a researcher/scientist identity. Auth-free — no password, no
-- verification; a profile only anchors run history to a person.
CREATE TABLE IF NOT EXISTS profiles (
    id          TEXT        PRIMARY KEY,
    name        TEXT        NOT NULL,
    email       TEXT        NOT NULL DEFAULT '',
    institution TEXT        NOT NULL DEFAULT '',
    field       TEXT        NOT NULL DEFAULT '',
    orcid       TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_profiles_created_at ON profiles (created_at DESC);

-- runs: the resistance-design run header + a link to the profile that owns it.
-- profile_id is nullable (anonymous runs) and ON DELETE SET NULL so deleting a
-- profile keeps its runs as orphans rather than destroying history.
CREATE TABLE IF NOT EXISTS runs (
    id           TEXT        PRIMARY KEY,
    profile_id   TEXT        REFERENCES profiles(id) ON DELETE SET NULL,
    uniprot_id   TEXT        NOT NULL,
    mutation     TEXT        NOT NULL,               -- canonical raw form, e.g. 'G12C'
    mutation_wt  TEXT        NOT NULL DEFAULT '',    -- parsed wild-type residue letter
    mutation_pos INTEGER     NOT NULL DEFAULT 0,     -- parsed 1-based position
    mutation_mut TEXT        NOT NULL DEFAULT '',    -- parsed mutant residue letter
    site_hint    TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL,               -- app vocabulary: structure_acquired | mutant_built | error
    error        TEXT        NOT NULL DEFAULT '',
    wt_structure JSONB,                              -- models.WTStructure (Stage 1)
    mutagenesis  JSONB,                              -- models.MutagenesisResult (Stage 2)
    pockets      JSONB,                              -- models.PocketAnalysis (Stage 3)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_runs_profile    ON runs (profile_id);
CREATE INDEX IF NOT EXISTS idx_runs_created_at ON runs (created_at DESC);

-- molecules: Claude-proposed candidates that passed RDKit validation (Stage 5/6),
-- tagged with the round that produced them (0 today; the loop adds rounds later).
-- UNIQUE (run_id, smiles_hash) dedupes a chemistry re-proposed within a run.
CREATE TABLE IF NOT EXISTS molecules (
    id          TEXT             PRIMARY KEY,
    run_id      TEXT             NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    round       INTEGER          NOT NULL DEFAULT 0,
    smiles      TEXT             NOT NULL,           -- RDKit-canonical SMILES
    smiles_hash TEXT             NOT NULL,           -- hex sha256 of the canonical SMILES
    inchikey    TEXT             NOT NULL DEFAULT '',
    qed         DOUBLE PRECISION NOT NULL DEFAULT 0,
    ro5_pass    BOOLEAN          NOT NULL DEFAULT false,
    sa_score    DOUBLE PRECISION,                    -- nullable: optional SA scorer
    mol_weight  DOUBLE PRECISION NOT NULL DEFAULT 0,
    logp        DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ      NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_molecules_run_smiles ON molecules (run_id, smiles_hash);
CREATE INDEX        IF NOT EXISTS idx_molecules_run       ON molecules (run_id);

-- docks: one molecule docked into BOTH tracks of a run (Stage 4). Kept as a
-- single paired row (both affinities + selectivity + both poses) to match the
-- app's models.LigandDock; poses are stored inline. UNIQUE (run_id, smiles_hash)
-- is the per-run per-chemistry cache the dock handler already enforces in memory.
CREATE TABLE IF NOT EXISTS docks (
    id              TEXT             PRIMARY KEY,
    run_id          TEXT             NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    smiles          TEXT             NOT NULL,
    smiles_hash     TEXT             NOT NULL,       -- hex sha256 of the SMILES
    wt_score        DOUBLE PRECISION NOT NULL DEFAULT 0,
    mutant_score    DOUBLE PRECISION NOT NULL DEFAULT 0,
    selectivity     DOUBLE PRECISION NOT NULL DEFAULT 0,
    wt_pose_pdb     TEXT             NOT NULL DEFAULT '',
    mutant_pose_pdb TEXT             NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ      NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_docks_run_smiles ON docks (run_id, smiles_hash);
CREATE INDEX        IF NOT EXISTS idx_docks_run       ON docks (run_id);
