package services

import (
	"math"
	"testing"

	"github.com/ayush00git/stanza/models"
)

func TestBoxSizeForScalesWithPocketVolume(t *testing.T) {
	small := boxSizeFor(models.Pocket{Volume: 463})  // KRAS switch-II pocket
	large := boxSizeFor(models.Pocket{Volume: 1071}) // KRAS nucleotide site
	if small > large {
		t.Fatalf("small pocket box %.1f must not exceed large pocket box %.1f", small, large)
	}
}

func TestBoxSizeForClamps(t *testing.T) {
	if got := boxSizeFor(models.Pocket{Volume: 1}); got != minBoxSize {
		t.Errorf("tiny pocket box = %.1f, want clamp to %.1f", got, minBoxSize)
	}
	if got := boxSizeFor(models.Pocket{Volume: 100000}); got != maxBoxSize {
		t.Errorf("huge pocket box = %.1f, want clamp to %.1f", got, maxBoxSize)
	}
	// A pocket with no recorded volume must not collapse the box to the padding.
	if got := boxSizeFor(models.Pocket{}); got != fallbackBoxSize {
		t.Errorf("volumeless pocket box = %.1f, want %.1f", got, fallbackBoxSize)
	}
	if got := boxSizeFor(models.Pocket{Volume: -5}); got != fallbackBoxSize {
		t.Errorf("negative volume box = %.1f, want %.1f", got, fallbackBoxSize)
	}
}

// The legacy box was a hardcoded 25 Å cube regardless of pocket. For a cryptic
// pocket like KRAS switch-II (463 Å^3) the box must come out meaningfully tighter:
// that tightening is what cut the seed-to-seed selectivity spread from sd 0.039 to
// sd 0.004 kcal/mol, against a margin of only ~0.15.
func TestBoxSizeForTightensOnCrypticPocket(t *testing.T) {
	const legacyBoxSize = 25.0
	if got := boxSizeFor(models.Pocket{Volume: 463}); got >= legacyBoxSize {
		t.Fatalf("switch-II box = %.1f A, want tighter than the legacy %.1f A cube", got, legacyBoxSize)
	}
}

// Within the clamps the box must actually enclose the pocket it is sized for,
// otherwise the padding is doing all the work and poses get truncated at the edge.
func TestBoxSizeForEnclosesPocketWithinClamps(t *testing.T) {
	for _, vol := range []float64{800, 1071, 1500} {
		diameter := 2 * math.Cbrt(3*vol/(4*math.Pi))
		if got := boxSizeFor(models.Pocket{Volume: vol}); got < diameter {
			t.Errorf("boxSizeFor(%.0f) = %.2f A, smaller than the pocket diameter %.2f A", vol, got, diameter)
		}
	}
}

func TestBoxSizeForMatchesSphereGeometry(t *testing.T) {
	// A pocket well inside the clamps must equal 2r + padding for an equal-volume
	// sphere, so the box tracks the cavity rather than a magic constant.
	const vol = 1071.0
	r := math.Cbrt(3 * vol / (4 * math.Pi))
	want := 2*r + boxPadding
	if got := boxSizeFor(models.Pocket{Volume: vol}); math.Abs(got-want) > 1e-9 {
		t.Errorf("boxSizeFor(%.0f) = %.4f, want %.4f", vol, got, want)
	}
	if want <= minBoxSize || want >= maxBoxSize {
		t.Fatalf("test fixture %.2f is not strictly inside the clamps; it proves nothing", want)
	}
}
