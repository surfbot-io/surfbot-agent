package scoring

import (
	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// Component describes the penalty applied for one severity level.
type Component struct {
	Severity string `json:"severity"`
	Count    int    `json:"count"`
	Weight   int    `json:"weight"`
	Penalty  int    `json:"penalty"`
}

// ComputeSecurityScore returns a 0–100 score and per-severity breakdown.
// The score starts at 100 and is reduced by count×weight for each severity.
func ComputeSecurityScore(sevCounts map[model.Severity]int) (int, []Component) {
	weights := []struct {
		sev    model.Severity
		weight int
	}{
		{model.SeverityCritical, 25},
		{model.SeverityHigh, 10},
		{model.SeverityMedium, 3},
		{model.SeverityLow, 1},
	}

	var breakdown []Component
	score := 100
	for _, w := range weights {
		count := sevCounts[w.sev]
		penalty := count * w.weight
		score -= penalty
		if count > 0 {
			breakdown = append(breakdown, Component{
				Severity: string(w.sev),
				Count:    count,
				Weight:   w.weight,
				Penalty:  penalty,
			})
		}
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, breakdown
}
