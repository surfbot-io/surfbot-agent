package intervalsched

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func TestBuildPipelineOptions_Full(t *testing.T) {
	opts := BuildPipelineOptions(ProfileFull, []string{"httpx"})
	require.Equal(t, model.ScanTypeFull, opts.ScanType)
	require.Empty(t, opts.Tools, "full profile must not restrict tools")
}

func TestBuildPipelineOptions_QuickDefault(t *testing.T) {
	opts := BuildPipelineOptions(ProfileQuick, nil)
	require.Equal(t, model.ScanTypeQuick, opts.ScanType)
	require.Equal(t, DefaultQuickTools, opts.Tools)
}

func TestBuildPipelineOptions_QuickCustom(t *testing.T) {
	opts := BuildPipelineOptions(ProfileQuick, []string{"httpx"})
	require.Equal(t, model.ScanTypeQuick, opts.ScanType)
	require.Equal(t, []string{"httpx"}, opts.Tools)
}
