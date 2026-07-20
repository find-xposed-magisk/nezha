//go:build linux

package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

type patRequest struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays int      `json:"expires_in_days"`
}

type patResponse struct {
	ID     uint64   `json:"id"`
	Token  string   `json:"token"`
	Scopes []string `json:"scopes"`
}

func (dashboard *Dashboard) bootstrapAuthentication(ctx context.Context) (patResponse, error) {
	readinessContext, cancel := context.WithTimeout(ctx, dashboard.readinessTimeout)
	defer cancel()
	retryTicker := time.NewTicker(dashboardRequestRetryPeriod)
	defer retryTicker.Stop()
	var login client.LoginResponse
	var err error
	for {
		login, err = dashboard.clients.REST.Login(readinessContext, client.LoginRequest{Username: "admin", Password: "admin"})
		if err == nil {
			break
		}
		select {
		case <-dashboard.supervisor.Exited():
			return patResponse{}, errors.New("dashboard process exited before login readiness")
		case <-readinessContext.Done():
			return patResponse{}, fmt.Errorf("dashboard login readiness: %w", errors.Join(err, readinessContext.Err()))
		case <-retryTicker.C:
		}
	}
	if login.Token == "" || login.Expire == "" {
		return patResponse{}, errors.New("dashboard login response omitted JWT metadata")
	}
	hasJWT, hasCSRF, err := dashboard.authenticationCookies()
	if err != nil {
		return patResponse{}, err
	}
	if !hasJWT || !hasCSRF {
		return patResponse{}, errors.New("dashboard login omitted authentication cookies")
	}
	pat, err := client.DoREST[patRequest, patResponse](readinessContext, dashboard.clients.REST, client.RESTRequest[patRequest]{
		Method: http.MethodPost,
		Path:   "/api/v1/api-tokens",
		Body: &patRequest{
			Name:          "agentcompat-admin",
			Scopes:        []string{"nezha:*"},
			ExpiresInDays: 0,
		},
	})
	if err != nil {
		return patResponse{}, fmt.Errorf("create dashboard PAT: %w", err)
	}
	if pat.ID == 0 || pat.Token == "" || len(pat.Scopes) != 1 || pat.Scopes[0] != "nezha:*" {
		return patResponse{}, errors.New("dashboard PAT response omitted wildcard administrator access")
	}
	dashboard.bootstrap = BootstrapResult{
		LoginAuthenticated: true,
		CSRFCookiePresent:  true,
		PATID:              pat.ID,
		PATScopes:          append([]string(nil), pat.Scopes...),
	}
	return pat, nil
}

func (dashboard *Dashboard) initializeAuthenticatedClients(ctx context.Context, pat patResponse) error {
	config := client.Config{BaseURL: dashboard.URL(), HTTPClient: dashboard.restHTTPClient, BearerToken: pat.Token}
	mcpClient, err := client.New(config)
	if err != nil {
		return err
	}
	webSocketClient, err := client.New(config)
	if err != nil {
		return err
	}
	dashboard.clients.MCP = mcpClient
	dashboard.clients.WebSocket = webSocketClient
	initializeResult, err := mcpClient.Initialize(ctx)
	if err != nil {
		return fmt.Errorf("initialize dashboard MCP: %w", err)
	}
	tools, err := mcpClient.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("list dashboard MCP tools: %w", err)
	}
	if initializeResult.ProtocolVersion != "2024-11-05" || initializeResult.ServerInfo.Name != "nezha-mcp" || len(tools.Tools) == 0 {
		return errors.New("dashboard MCP initialization returned incomplete capabilities")
	}
	dashboard.bootstrap.MCPProtocolVersion = initializeResult.ProtocolVersion
	dashboard.bootstrap.MCPServerName = initializeResult.ServerInfo.Name
	dashboard.bootstrap.MCPToolCount = len(tools.Tools)
	return nil
}

func (dashboard *Dashboard) authenticationCookies() (bool, bool, error) {
	baseURL, err := url.Parse(dashboard.URL())
	if err != nil {
		return false, false, fmt.Errorf("parse dashboard URL: %w", err)
	}
	var hasJWT bool
	var hasCSRF bool
	for _, cookie := range dashboard.restHTTPClient.Jar.Cookies(baseURL) {
		switch cookie.Name {
		case "nz-jwt":
			hasJWT = cookie.Value != ""
		case "nz-csrf":
			hasCSRF = cookie.Value != ""
		}
	}
	return hasJWT, hasCSRF, nil
}
