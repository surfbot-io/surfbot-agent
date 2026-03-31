package detection

import (
	"testing"

	nucleimodel "github.com/projectdiscovery/nuclei/v3/pkg/model"
	"github.com/projectdiscovery/nuclei/v3/pkg/model/types/severity"
	"github.com/projectdiscovery/nuclei/v3/pkg/model/types/stringslice"
	"github.com/projectdiscovery/nuclei/v3/pkg/output"
	"github.com/stretchr/testify/assert"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

func TestNucleiResultMapping(t *testing.T) {
	event := &output.ResultEvent{
		TemplateID: "ssl-expired",
		Matched:    "https://example.com:443",
		Info: nucleimodel.Info{
			Name:        "Expired SSL Certificate",
			Description: "The SSL certificate has expired",
			Remediation: "Renew the certificate",
			SeverityHolder: severity.Holder{
				Severity: severity.Medium,
			},
			Reference: stringslice.NewRawStringSlice([]string{"https://cwe.mitre.org/data/definitions/295.html"}),
		},
	}

	finding := mapNucleiEvent(event)

	assert.Equal(t, "ssl-expired", finding.TemplateID)
	assert.Equal(t, "Expired SSL Certificate", finding.Title)
	assert.Equal(t, model.SeverityMedium, finding.Severity)
	assert.Equal(t, "The SSL certificate has expired", finding.Description)
	assert.Equal(t, "Renew the certificate", finding.Remediation)
	assert.Equal(t, "https://example.com:443", finding.Evidence)
	assert.Equal(t, "nuclei", finding.SourceTool)
	assert.Equal(t, 80.0, finding.Confidence)
	assert.NotEmpty(t, finding.References)
}

func TestNucleiCVEExtraction(t *testing.T) {
	event := &output.ResultEvent{
		TemplateID: "CVE-2021-44228",
		Matched:    "http://target:8080",
		Info: nucleimodel.Info{
			Name: "Apache Log4j RCE",
			SeverityHolder: severity.Holder{
				Severity: severity.Critical,
			},
			Classification: &nucleimodel.Classification{
				CVEID:     stringslice.New("CVE-2021-44228"),
				CVSSScore: 10.0,
			},
		},
	}

	finding := mapNucleiEvent(event)

	assert.Equal(t, "CVE-2021-44228", finding.CVE)
	assert.Equal(t, 10.0, finding.CVSS)
	assert.Equal(t, model.SeverityCritical, finding.Severity)
}

func TestNucleiAvailable(t *testing.T) {
	n := NewNucleiTool()
	assert.True(t, n.Available(), "nuclei library tool is always available")
	assert.Equal(t, ToolKindLibrary, n.Kind())
}

func TestNucleiSeverityMapping(t *testing.T) {
	tests := []struct {
		input    string
		expected model.Severity
	}{
		{"critical", model.SeverityCritical},
		{"high", model.SeverityHigh},
		{"medium", model.SeverityMedium},
		{"low", model.SeverityLow},
		{"info", model.SeverityInfo},
		{"unknown", model.SeverityInfo},
		{"CRITICAL", model.SeverityCritical},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, mapNucleiSeverity(tc.input))
		})
	}
}
