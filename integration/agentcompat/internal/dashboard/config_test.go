//go:build linux

package dashboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDashboardConfig_UsesDeterministicHermeticSettings(t *testing.T) {
	// Given
	configPath := filepath.Join(t.TempDir(), "dashboard.yaml")

	// When
	err := writeDashboardConfig(configPath, dashboardConfig{
		HTTPAddress:     "127.0.0.1:18008",
		HTTPSAddress:    "127.0.0.1:18443",
		CertificatePath: "/tmp/dashboard.crt",
		KeyPath:         "/tmp/dashboard.key",
	})

	// Then
	require.NoError(t, err)
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	content := string(data)
	require.Contains(t, content, "listen_host: 127.0.0.1")
	require.Contains(t, content, "listen_port: 18008")
	require.Contains(t, content, "location: UTC")
	require.Contains(t, content, "force_auth: true")
	require.Contains(t, content, "enable_mcp: true")
	require.Contains(t, content, "oauth2: {}")
	require.Contains(t, content, "data_path: \"\"")
	require.Contains(t, content, "listen_port: 18443")
	require.Contains(t, content, "insecure_tls: false")
	require.NotContains(t, content, jwtSecret)
}

func TestDashboardEnvironment_RemovesAmbientNezhaOverrides(t *testing.T) {
	// Given
	t.Setenv("NZ_FORCEAUTH", "false")
	t.Setenv("NZ_ENABLEMCP", "false")
	t.Setenv("NEZHA_AGENTCOMPAT_HTTP_LISTENER_FD", "999")

	// When
	environment := dashboardEnvironment(true)

	// Then
	require.Contains(t, environment, "NZ_JWTSECRETKEY="+jwtSecret)
	require.Contains(t, environment, "NEZHA_AGENTCOMPAT_HTTP_LISTENER_FD=3")
	require.Contains(t, environment, "NEZHA_AGENTCOMPAT_HTTPS_LISTENER_FD=4")
	require.NotContains(t, environment, "NZ_FORCEAUTH=false")
	require.NotContains(t, environment, "NZ_ENABLEMCP=false")
	require.NotContains(t, environment, "NEZHA_AGENTCOMPAT_HTTP_LISTENER_FD=999")
}

func TestDashboardStart_RejectsRelativeSourceDirectory(t *testing.T) {
	// When
	_, err := Start(t.Context(), StartConfig{SourceDir: "../nezha"})

	// Then
	require.ErrorContains(t, err, "source directory must be absolute")
}
