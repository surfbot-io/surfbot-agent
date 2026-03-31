package detection

import (
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func buildToolRun(tool DetectionTool, startedAt time.Time, status model.ToolRunStatus, errMsg string, targetsCount, findingsCount int) model.ToolRun {
	now := time.Now().UTC()
	return model.ToolRun{
		ID:            uuid.New().String(),
		ToolName:      tool.Name(),
		Phase:         tool.Phase(),
		Status:        status,
		StartedAt:     startedAt,
		FinishedAt:    &now,
		DurationMs:    now.Sub(startedAt).Milliseconds(),
		TargetsCount:  targetsCount,
		FindingsCount: findingsCount,
		ErrorMessage:  errMsg,
		Config:        map[string]interface{}{},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}
