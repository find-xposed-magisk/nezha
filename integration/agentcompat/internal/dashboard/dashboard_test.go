//go:build linux

package dashboard

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/testpaths"
	"github.com/nezhahq/nezha/model"
)

func TestDashboard_BootstrapsSQLiteLoginPATAndMCP(t *testing.T) {
	// Given
	dashboard := startDashboard(t, false)
	bootstrap := dashboard.Bootstrap()

	// When
	database, err := gorm.Open(sqlite.Open(dashboard.DatabasePath()), &gorm.Config{})
	require.NoError(t, err)
	var userCount int64
	require.NoError(t, database.Model(&model.User{}).Count(&userCount).Error)
	var tokenCount int64
	require.NoError(t, database.Model(&model.APIToken{}).Count(&tokenCount).Error)
	require.True(t, database.Migrator().HasTable(&model.MCPAuditLog{}))
	sqlDatabase, err := database.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDatabase.Close())
	configData, err := os.ReadFile(dashboard.ConfigPath())
	require.NoError(t, err)
	logData, err := os.ReadFile(dashboard.LogPath())
	require.NoError(t, err)
	jwtToken := requireJWTSignedWithDeterministicSecret(t, dashboard.Clients().REST)
	unauthenticatedStatus, unauthenticatedResponse := requestUnauthenticatedInventory(t, dashboard)

	// Then
	require.Equal(t, int64(1), userCount)
	require.Equal(t, int64(1), tokenCount)
	require.True(t, bootstrap.LoginAuthenticated)
	require.True(t, bootstrap.CSRFCookiePresent)
	require.NotZero(t, bootstrap.PATID)
	require.Equal(t, []string{"nezha:*"}, bootstrap.PATScopes)
	require.Equal(t, "2024-11-05", bootstrap.MCPProtocolVersion)
	require.Equal(t, "nezha-mcp", bootstrap.MCPServerName)
	require.Positive(t, bootstrap.MCPToolCount)
	require.Equal(t, http.StatusOK, unauthenticatedStatus)
	require.False(t, unauthenticatedResponse.Success)
	require.Contains(t, unauthenticatedResponse.Error, "Unauthorized")
	require.Len(t, agentSecret, 32)
	require.Contains(t, string(configData), "force_auth: true")
	require.Contains(t, string(configData), "enable_mcp: true")
	require.Contains(t, string(configData), "agent_secret_key: \""+agentSecret+"\"")
	require.NotContains(t, string(configData), jwtSecret)
	require.NotContains(t, string(logData), jwtSecret)
	require.NotContains(t, string(logData), agentSecret)
	require.NotContains(t, string(logData), jwtToken)
	require.NotContains(t, string(logData), "nzp_")
	require.NotEqual(t, dashboard.ConfigPath(), dashboard.DatabasePath())
	require.FileExists(t, dashboard.DatabasePath())
	require.NotNil(t, dashboard.Clients().REST)
	require.NotNil(t, dashboard.Clients().MCP)
	require.NotNil(t, dashboard.Clients().WebSocket)
}

func TestDashboard_ServesTrustedTLS(t *testing.T) {
	// Given
	dashboard := startDashboard(t, true)
	bootstrap := dashboard.Bootstrap()

	// When
	wrongHostClient, wrongHostTransport, err := dashboard.newTLSHTTPClient("wronghost.invalid")
	require.NoError(t, err)
	defer wrongHostTransport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, dashboard.TLSURL()+"/api/v1/login", strings.NewReader(`{"username":"admin","password":"admin"}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	_, err = wrongHostClient.Do(request)

	// Then
	require.True(t, bootstrap.TLSAuthenticated)
	require.False(t, dashboard.tlsFixture.ClientConfig("localhost").InsecureSkipVerify)
	var hostnameError x509.HostnameError
	require.ErrorAs(t, err, &hostnameError)
}

func TestDashboard_RejectsWrongLogin(t *testing.T) {
	// Given
	dashboard := startDashboard(t, false)

	// When
	_, err := dashboard.Clients().REST.Login(t.Context(), client.LoginRequest{Username: "admin", Password: "wrong-password"})

	// Then
	require.ErrorIs(t, err, client.ErrSemanticFailure)
	require.ErrorContains(t, err, "Unauthorized")
}

func TestDashboard_RejectsMalformedCSRF(t *testing.T) {
	// Given
	dashboard := startDashboard(t, false)

	// When
	status, responseBody := postMalformedCSRF(t, dashboard)

	// Then
	require.Equal(t, http.StatusForbidden, status)
	var envelope client.CommonResponse[json.RawMessage]
	require.NoError(t, json.Unmarshal(responseBody, &envelope))
	require.False(t, envelope.Success)
	require.Contains(t, envelope.Error, "invalid CSRF token")
}

func TestDashboard_StopsCleanly(t *testing.T) {
	// Given
	dashboard := startDashboardWithoutCleanup(t, false)
	root := dashboard.WorkspaceRoot()
	pid := dashboard.PID()

	// When
	stopContext, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	require.NoError(t, dashboard.Stop(stopContext))

	// Then
	receipt := dashboard.CleanupReceipt()
	require.True(t, receipt.Passed)
	require.False(t, receipt.Forced)
	require.Len(t, receipt.Processes, 1)
	require.NoDirExists(t, root)
	require.NoFileExists(t, filepath.Join("/proc", strconv.Itoa(pid)))
}

func startDashboard(t *testing.T, enableTLS bool) *Dashboard {
	t.Helper()
	dashboard := startDashboardWithoutCleanup(t, enableTLS)
	t.Cleanup(func() {
		stopContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, dashboard.Stop(stopContext))
	})
	return dashboard
}

func startDashboardWithoutCleanup(t *testing.T, enableTLS bool) *Dashboard {
	t.Helper()
	sourceDir, err := testpaths.NezhaSource(t.Name())
	require.NoError(t, err)
	dashboard, err := Start(t.Context(), StartConfig{SourceDir: sourceDir, EnableTLS: enableTLS})
	require.NoError(t, err)
	return dashboard
}

func postMalformedCSRF(t *testing.T, dashboard *Dashboard) (int, []byte) {
	t.Helper()
	requestBody, err := json.Marshal(patRequest{Name: "malformed-csrf", Scopes: []string{"nezha:*"}})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, dashboard.URL()+"/api/v1/api-tokens", bytes.NewReader(requestBody))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "malformed")
	response, err := dashboard.restHTTPClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	require.NoError(t, err)
	return response.StatusCode, responseBody
}

func requireJWTSignedWithDeterministicSecret(t *testing.T, restClient *client.Client) string {
	t.Helper()
	login, err := restClient.Login(t.Context(), client.LoginRequest{Username: "admin", Password: "admin"})
	require.NoError(t, err)
	parsed, err := jwt.Parse(login.Token, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected JWT algorithm")
		}
		return []byte(jwtSecret), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	return login.Token
}

func requestUnauthenticatedInventory(t *testing.T, dashboard *Dashboard) (int, client.CommonResponse[json.RawMessage]) {
	t.Helper()
	transport := &http.Transport{DialContext: dialAddress(dashboard.httpAddress)}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: dashboardHTTPClientTimeout}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, dashboard.URL()+"/api/v1/server", nil)
	require.NoError(t, err)
	response, err := httpClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	require.NoError(t, err)
	var envelope client.CommonResponse[json.RawMessage]
	require.NoError(t, json.Unmarshal(responseBody, &envelope))
	return response.StatusCode, envelope
}
