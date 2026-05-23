package assembler

import "testing"

func TestComputeConfidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		independentSourceCount int
		sourceConfidences      []float64
		want                   float64
	}{
		{
			name:                   "single source defaults to 0.5",
			independentSourceCount: 1,
			sourceConfidences:      []float64{},
			want:                   0.5,
		},
		{
			name:                   "low source confidence stays lower than ceiling",
			independentSourceCount: 1,
			sourceConfidences:      []float64{0.3},
			want:                   0.3,
		},
		{
			name:                   "two independent sources ceiling",
			independentSourceCount: 2,
			sourceConfidences:      []float64{0.9},
			want:                   0.85,
		},
		{
			name:                   "three independent sources ceiling",
			independentSourceCount: 3,
			sourceConfidences:      []float64{0.92, 0.99},
			want:                   0.95,
		},
		{
			name:                   "default ceiling applies when no source confidences are present",
			independentSourceCount: 4,
			sourceConfidences:      []float64{},
			want:                   0.95,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ComputeConfidence(tt.independentSourceCount, tt.sourceConfidences)
			if got != tt.want {
				t.Fatalf("ComputeConfidence() = %v, want %v", got, tt.want)
			}
		})
	}
}
