-- Where a docked molecule came from: "claude" (a generated candidate) or "chembl" (a
-- fetched reference compound docked as a control). Empty on docks written before this
-- migration; the leaderboard treats empty as unknown and does not guess.
ALTER TABLE docks
    ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT '';
