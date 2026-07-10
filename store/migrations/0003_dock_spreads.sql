-- Error bars on the docking affinities.
--
-- Selectivity = wt_score - mutant_score was published with no measure of the search
-- noise that produced it. One molecule read +2.39 kcal/mol against a mutation that
-- cannot change reversible binding: a seven-seed scan showed both tracks were bimodal,
-- and the wild-type track found its deep pose in one seed of seven. The two pockets
-- bound the ligand to within 0.19 kcal/mol.
--
-- wt_spread / mutant_spread are max - min affinity over that track's docking seeds.
-- replicates is the seed count. Rows written before this migration carry replicates = 0,
-- which means "the spread is unknown", NOT "the spread was zero" — the two must not be
-- confused, so there is no backfill and no non-zero default.

ALTER TABLE docks
    ADD COLUMN IF NOT EXISTS wt_spread     double precision NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS mutant_spread double precision NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS replicates    integer          NOT NULL DEFAULT 0;
