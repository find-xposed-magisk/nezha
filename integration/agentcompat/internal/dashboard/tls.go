//go:build linux

package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

func (dashboard *Dashboard) verifyTrustedTLS(ctx context.Context) error {
	httpClient, transport, err := dashboard.newTLSHTTPClient("localhost")
	if err != nil {
		return err
	}
	dashboard.tlsTransport = transport
	tlsClient, err := client.New(client.Config{BaseURL: dashboard.TLSURL(), HTTPClient: httpClient})
	if err != nil {
		return err
	}
	login, err := tlsClient.Login(ctx, client.LoginRequest{Username: "admin", Password: "admin"})
	if err != nil {
		return fmt.Errorf("login through trusted dashboard TLS: %w", err)
	}
	if login.Token == "" {
		return errors.New("trusted dashboard TLS login omitted JWT")
	}
	dashboard.bootstrap.TLSAuthenticated = true
	return nil
}

func (dashboard *Dashboard) newTLSHTTPClient(serverName string) (*http.Client, *http.Transport, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create TLS cookie jar: %w", err)
	}
	transport := &http.Transport{
		TLSClientConfig: dashboard.tlsFixture.ClientConfig(serverName),
		DialContext:     dialAddress(dashboard.httpsAddress),
	}
	return &http.Client{Transport: transport, Jar: jar, Timeout: dashboardHTTPClientTimeout}, transport, nil
}
