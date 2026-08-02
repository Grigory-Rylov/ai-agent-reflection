package compress

import "testing"

func TestCalculateCompressionRatio(t *testing.T) {
	t.Run("normal ratio", func(t *testing.T) {
		ratio := CalculateCompressionRatio(1000, 500)
		if ratio != 0.5 {
			t.Errorf("expected 0.5, got %f", ratio)
		}
	})

	t.Run("original is 0", func(t *testing.T) {
		ratio := CalculateCompressionRatio(0, 100)
		if ratio != 1.0 {
			t.Errorf("expected 1.0, got %f", ratio)
		}
	})
}
