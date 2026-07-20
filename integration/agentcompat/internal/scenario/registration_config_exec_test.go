//go:build linux && agentcompat

package scenario

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

func TestConfigDiff_ChangesOnlyDebugAndReportDelay(t *testing.T) {
	// Given
	original := AgentConfigSnapshot{Debug: false, ReportDelay: 1, ClientSecret: "secret", UUID: "uuid", Server: "server"}
	updated := original
	updated.Debug = true
	updated.ReportDelay = 2

	// When
	diff, err := ConfigDiff(original, updated)

	// Then
	require.NoError(t, err)
	require.Equal(t, ConfigDiffResult{DebugChanged: true, ReportDelayChanged: true}, diff)
}

func TestConfigDiff_RejectsCredentialIdentityAndEndpointChanges(t *testing.T) {
	// Given
	original := AgentConfigSnapshot{ClientSecret: "secret", UUID: "uuid", Server: "server", ReportDelay: 1}
	updated := original
	updated.ClientSecret = "other-secret"

	// When
	_, err := ConfigDiff(original, updated)

	// Then
	require.ErrorIs(t, err, ErrConfigIdentityChanged)
}

func TestSensitiveEvidence_RedactsConfigCredentials(t *testing.T) {
	// Given
	secret := "0123456789abcdef0123456789abcdef"
	config := `{"client_secret":"` + secret + `","server":"127.0.0.1:5555","debug":true}`

	// When
	redacted := evidence.Redact(config)

	// Then
	require.NotContains(t, redacted, secret)
	require.Contains(t, redacted, "[REDACTED]")
}

func TestFinish_RecordsFailedAssertionAndErrorForCleanup(t *testing.T) {
	assertions := NewAssertionSet()
	assertions.Record("readiness", false, "invalid secret prevented readiness")

	result, err := finish(assertions, nil)

	require.EqualError(t, err, "readiness: invalid secret prevented readiness")
	require.False(t, result.Passed)
	require.False(t, result.CleanupOK)
	require.Equal(t, "readiness: invalid secret prevented readiness", result.Error)
	require.Len(t, result.Assertions, 1)
	require.False(t, result.Assertions[0].Passed)
}
