package services

import (
	"encoding/json"
	"strings"
	"testing"
)

func f(v float64) *float64 { return &v }

// The pre-filter is the quietest stage in the pipeline. A round where Claude proposes 8
// molecules and the board shows 2 must be able to say which gate ate the other six, and
// with what number — a count alone cannot distinguish "too light" from "too heavy", and
// those imply opposite fixes to the prompt.
func TestSummarizeValidationRecordsWhatWasDropped(t *testing.T) {
	verdicts := []MoleculeVerdict{
		{SMILES: "CCO", Kept: true, Valid: true, MolWeight: f(46.1), QED: f(0.41)},
		{SMILES: "c1ccccc1", Valid: true, DropReason: "mw_out_of_range", MolWeight: f(78.1), QED: f(0.44)},
		{SMILES: "not_a_molecule", DropReason: "invalid_smiles"},
		{SMILES: "CCO", Valid: true, DropReason: "duplicate", MolWeight: f(46.1), QED: f(0.41)},
		{SMILES: "C" + "C(=O)N", Valid: true, DropReason: "mw_out_of_range", MolWeight: f(999.0), QED: f(0.10)},
	}
	th := &ValidationThresholds{MWMin: 430, MWMax: 620, QEDMin: 0.25}

	kept, s := summarizeValidation(verdicts, th)

	if len(kept) != 1 || kept[0].SMILES != "CCO" {
		t.Fatalf("kept = %+v, want exactly the one valid molecule", kept)
	}
	if s.Proposed != 5 || s.Kept != 1 {
		t.Errorf("proposed=%d kept=%d, want 5 and 1", s.Proposed, s.Kept)
	}
	if s.Dropped["mw_out_of_range"] != 2 || s.Dropped["invalid_smiles"] != 1 || s.Dropped["duplicate"] != 1 {
		t.Errorf("dropped tally = %v, want mw:2 invalid:1 duplicate:1", s.Dropped)
	}

	// Details must preserve proposal order and carry the disqualifying number, so the UI
	// can say "too light, 78.1 Da" rather than "molecular weight out of range".
	if len(s.Details) != 4 {
		t.Fatalf("details = %d, want 4 (one per drop)", len(s.Details))
	}
	if s.Details[0].Reason != "mw_out_of_range" || s.Details[0].MolWeight == nil || *s.Details[0].MolWeight != 78.1 {
		t.Errorf("first drop = %+v, want the benzene with its weight attached", s.Details[0])
	}
	if s.Details[1].MolWeight != nil {
		t.Errorf("an unparseable SMILES has no weight; got %v", *s.Details[1].MolWeight)
	}
	// The last drop is heavy, not light: same reason, opposite fix.
	if last := s.Details[3]; last.MolWeight == nil || *last.MolWeight != 999.0 {
		t.Errorf("last drop = %+v, want the 999 Da molecule", last)
	}

	// The window has to reach the client, or it cannot name what a molecule missed.
	if s.MWMin != 430 || s.MWMax != 620 || s.QEDMin != 0.25 {
		t.Errorf("thresholds not echoed: %+v", s)
	}
}

// Without a curated site the script's defaults apply and no window is claimed. Reporting
// a zero window would render as "outside 0–0 Da".
func TestSummarizeValidationOmitsAbsentThresholds(t *testing.T) {
	_, s := summarizeValidation([]MoleculeVerdict{{SMILES: "CCO", Kept: true}}, nil)
	if s.MWMin != 0 || s.MWMax != 0 || s.QEDMin != 0 {
		t.Errorf("nil thresholds produced a window: %+v", s)
	}

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"mw_min", "mw_max", "qed_min", "dropped", "details"} {
		if _, present := got[k]; present {
			t.Errorf("%q serialized despite being empty; the UI keys off its absence", k)
		}
	}
	if got["proposed"] != float64(1) || got["kept"] != float64(1) {
		t.Errorf("proposed/kept missing from JSON: %v", got)
	}
}

// Told "430–620 Da" as advice, the generator returned 386, 401, 409, 409, 412, 419, 421
// and 448 Da — seven of eight just under the floor, seven deleted before docking. The
// brief must say the gate deletes, must quote real masses to calibrate against, and must
// aim at the middle of the window rather than its edge.
func TestWeightGateIsStatedAsAGateNotAPreference(t *testing.T) {
	g := &SiteGuidance{
		MinMW: 430, MaxMW: 620,
		MassAnchors: []MassAnchor{{"ARS-1620", 430.8}, {"sotorasib", 560.6}},
	}
	var b strings.Builder
	writeWeightGate(&b, g)
	got := b.String()

	for _, want := range []string{
		"HARD GATE",  // not a suggestion
		"DISCARDED",  // states the consequence
		"485–565 Da", // the midpoint band (525 ± 40), not the floor
		"ARS-1620: 430.8 Da",
		"sotorasib: 560.6 Da",
		"heavy atom", // the counting heuristic
	} {
		if !strings.Contains(got, want) {
			t.Errorf("brief is missing %q; full text:\n%s", want, got)
		}
	}

	// The target band must sit strictly inside the window, or the advice contradicts the gate.
	if !strings.Contains(got, "430–620 Da") {
		t.Errorf("brief never states the window itself:\n%s", got)
	}
}

// A site with no anchors must not emit an empty calibration list.
func TestWeightGateOmitsAnchorsWhenAbsent(t *testing.T) {
	var b strings.Builder
	writeWeightGate(&b, &SiteGuidance{MinMW: 300, MaxMW: 500})
	if strings.Contains(b.String(), "Calibrate against real masses") {
		t.Errorf("anchor block emitted with no anchors:\n%s", b.String())
	}
}
