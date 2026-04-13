package scoring

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func TestComputeSecurityScore(t *testing.T) {
	tests := []struct {
		name      string
		counts    map[model.Severity]int
		wantScore int
		wantLen   int
	}{
		{
			name:      "no findings gives 100",
			counts:    map[model.Severity]int{},
			wantScore: 100,
			wantLen:   0,
		},
		{
			name:      "one critical deducts 25",
			counts:    map[model.Severity]int{model.SeverityCritical: 1},
			wantScore: 75,
			wantLen:   1,
		},
		{
			name:      "mixed severities",
			counts:    map[model.Severity]int{model.SeverityCritical: 1, model.SeverityHigh: 2, model.SeverityLow: 5},
			wantScore: 50,
			wantLen:   3,
		},
		{
			name:      "clamp at zero",
			counts:    map[model.Severity]int{model.SeverityCritical: 10},
			wantScore: 0,
			wantLen:   1,
		},
		{
			name:      "info findings have no penalty",
			counts:    map[model.Severity]int{model.SeverityInfo: 100},
			wantScore: 100,
			wantLen:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, breakdown := ComputeSecurityScore(tt.counts)
			assert.Equal(t, tt.wantScore, score)
			assert.Len(t, breakdown, tt.wantLen)
		})
	}
}
