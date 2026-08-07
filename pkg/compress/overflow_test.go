package compress

import (
	"testing"
)

// TestUsable_ModelLimitInput проверяет что Usable поддерживает model.limit.input
// отдельно от context, как в opencode overflow.ts:
//   model.limit.input ? max(0, input - reserved) : max(0, context - maxOutputTokens)
func TestUsable_ModelLimitInput(t *testing.T) {
	tests := []struct {
		name         string
		contextLimit int
		inputLimit   int
		reserved     *int
		want         int
	}{
		{
			name:         "no input limit uses context minus maxOutputTokens",
			contextLimit: 200_000,
			inputLimit:   0,
			reserved:     nil,
			want:         168_000, // 200000 - OUTPUT_TOKEN_MAX(32000)
		},
		{
			name:         "with input limit uses input minus reserved",
			contextLimit: 200_000,
			inputLimit:   150_000,
			reserved:     nil,
			want:         130_000, // 150000 - 20000
		},
		{
			name:         "custom reserved with input limit",
			contextLimit: 200_000,
			inputLimit:   150_000,
			reserved:     intPtr(30_000),
			want:         120_000, // 150000 - 30000
		},
		{
			name:         "zero context returns 0",
			contextLimit: 0,
			inputLimit:   150_000,
			reserved:     nil,
			want:         0,
		},
		{
			name:         "reserved larger than input limit returns 0",
			contextLimit: 200_000,
			inputLimit:   10_000,
			reserved:     intPtr(30_000),
			want:         0, // max(0, 10000 - 30000) = 0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UsableWithLimits(tt.contextLimit, tt.inputLimit, tt.reserved)
			if got != tt.want {
				t.Errorf("UsableWithLimits(%d, %d, %v) = %d, want %d",
					tt.contextLimit, tt.inputLimit, tt.reserved, got, tt.want)
			}
		})
	}
}

// TestIsOverflow_ProviderTokens проверяет что IsOverflow учитывает
// provider-reported tokens (total || input+output+cache), как в opencode.
func TestIsOverflow_ProviderTokens(t *testing.T) {
	tests := []struct {
		name          string
		contextLimit  int
		inputLimit    int
		tokens        ProviderTokens
		wantOverflow  bool
	}{
		{
			name:         "total tokens available",
			contextLimit: 200_000,
			inputLimit:   0,
			tokens:       ProviderTokens{Total: 190_000},
			wantOverflow: true, // 190000 >= usable(168000)
		},
		{
			name:         "total tokens under limit",
			contextLimit: 200_000,
			inputLimit:   0,
			tokens:       ProviderTokens{Total: 100_000},
			wantOverflow: false, // 100000 < 168000
		},
		{
			name:         "total 0 falls back to input+output",
			contextLimit: 200_000,
			inputLimit:   0,
			tokens:       ProviderTokens{Input: 150_000, Output: 10_000},
			wantOverflow: false, // 160000 < 168000
		},
		{
			name:         "input+output+cache exceeds limit",
			contextLimit: 200_000,
			inputLimit:   0,
			tokens:       ProviderTokens{Input: 150_000, Output: 30_000, CacheRead: 5_000},
			wantOverflow: true, // 185000 >= 168000
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsOverflowWithProviderTokens(tt.tokens, tt.contextLimit, tt.inputLimit, nil)
			if got != tt.wantOverflow {
				t.Errorf("IsOverflowWithProviderTokens = %v, want %v (tokens.total=%d, sum=%d, usable=%d)",
					got, tt.wantOverflow,
					tt.tokens.Total,
					tt.tokens.Input+tt.tokens.Output+tt.tokens.CacheRead+tt.tokens.CacheWrite,
					UsableWithLimits(tt.contextLimit, tt.inputLimit, nil))
			}
		})
	}
}


