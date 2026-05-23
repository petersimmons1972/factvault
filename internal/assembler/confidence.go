package assembler

const (
	oneSourceCeiling   = 0.5
	twoSourceCeiling   = 0.85
	multiSourceCeiling = 0.95
)

// ComputeConfidence applies the deterministic corroboration ceiling contract.
func ComputeConfidence(independentSourceCount int, sourceConfidences []float64) float64 {
	ceiling := corroborationCeiling(independentSourceCount)
	perSourceMax := multiSourceCeiling
	if len(sourceConfidences) > 0 {
		perSourceMax = sourceConfidences[0]
		for _, confidence := range sourceConfidences[1:] {
			if confidence > perSourceMax {
				perSourceMax = confidence
			}
		}
	}
	if perSourceMax < ceiling {
		return perSourceMax
	}
	return ceiling
}

func corroborationCeiling(independentSourceCount int) float64 {
	switch {
	case independentSourceCount <= 1:
		return oneSourceCeiling
	case independentSourceCount == 2:
		return twoSourceCeiling
	default:
		return multiSourceCeiling
	}
}
