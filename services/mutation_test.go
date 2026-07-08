package services

import "testing"

func TestParseMutationValid(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantWildType string
		wantPosition int
		wantMutant   string
		wantRaw      string
	}{
		{"canonical", "G12C", "G", 12, "C", "G12C"},
		{"lowercase normalized", "t790m", "T", 790, "M", "T790M"},
		{"surrounding whitespace", "  L858R  ", "L", 858, "R", "L858R"},
		{"multi-digit position", "V600E", "V", 600, "E", "V600E"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMutation(tt.raw)
			if err != nil {
				t.Fatalf("ParseMutation(%q) returned unexpected error: %v", tt.raw, err)
			}
			if got.WildType != tt.wantWildType {
				t.Errorf("WildType = %q, want %q", got.WildType, tt.wantWildType)
			}
			if got.Position != tt.wantPosition {
				t.Errorf("Position = %d, want %d", got.Position, tt.wantPosition)
			}
			if got.Mutant != tt.wantMutant {
				t.Errorf("Mutant = %q, want %q", got.Mutant, tt.wantMutant)
			}
			if got.Raw != tt.wantRaw {
				t.Errorf("Raw = %q, want %q", got.Raw, tt.wantRaw)
			}
			if got.String() != tt.wantRaw {
				t.Errorf("String() = %q, want %q", got.String(), tt.wantRaw)
			}
		})
	}
}

func TestParseMutationErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"missing mutant", "G12"},
		{"non-amino-acid letter", "GXC"},
		{"invalid wild-type B", "B12C"},
		{"missing wild-type", "12C"},
		{"non-positive position", "G0C"},
		{"same wild-type and mutant", "G12G"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMutation(tt.raw)
			if err == nil {
				t.Fatalf("ParseMutation(%q) = %+v, want error", tt.raw, got)
			}
		})
	}
}
