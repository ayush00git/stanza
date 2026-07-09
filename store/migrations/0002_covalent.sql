-- Stage-4 covalent docking: the mutant track can model a covalent tether to the
-- mutated cysteine. Store that record (warhead, reach, credit, raw score) as one
-- JSON blob beside the paired dock scores. NULL for non-covalent docks.
ALTER TABLE docks ADD COLUMN IF NOT EXISTS covalent JSONB;
