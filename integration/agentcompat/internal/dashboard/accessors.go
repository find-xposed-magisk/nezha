//go:build linux

package dashboard

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

func (dashboard *Dashboard) TLSURL() string {
	if dashboard.httpsAddress == "" {
		return ""
	}
	_, port, err := splitAddress(dashboard.httpsAddress)
	if err != nil {
		return ""
	}
	return "https://localhost:" + port
}

func (dashboard *Dashboard) TLSCACertificatePath() string {
	if dashboard.tlsFixture.CAPEM() == nil {
		return ""
	}
	return filepath.Join(dashboard.workspace.Root(), "dashboard-ca.crt")
}

func (dashboard *Dashboard) Clients() Clients { return dashboard.clients }

func (dashboard *Dashboard) AuthenticatedClient(token string) (*client.Client, error) {
	return client.New(client.Config{BaseURL: dashboard.URL(), HTTPClient: dashboard.restHTTPClient, BearerToken: token})
}

func (dashboard *Dashboard) ReleaseReceipt(ctx context.Context) error {
	if dashboard.receiptConn == nil {
		return errors.New("receipt gate is disabled")
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := dashboard.receiptConn.SetWriteDeadline(deadline); err != nil {
			return err
		}
	}
	_, err := dashboard.receiptConn.Write([]byte("release\n"))
	return err
}

func (dashboard *Dashboard) Bootstrap() BootstrapResult {
	result := dashboard.bootstrap
	result.PATScopes = append([]string(nil), result.PATScopes...)
	return result
}

func (dashboard *Dashboard) AgentSecret() string { return agentSecret }

func (dashboard *Dashboard) ConfigPath() string    { return dashboard.configPath }
func (dashboard *Dashboard) DatabasePath() string  { return dashboard.databasePath }
func (dashboard *Dashboard) LogPath() string       { return dashboard.logPath }
func (dashboard *Dashboard) WorkspaceRoot() string { return dashboard.workspace.Root() }

func (dashboard *Dashboard) PID() int {
	if dashboard.supervisor == nil {
		return 0
	}
	return dashboard.supervisor.PID()
}

func (dashboard *Dashboard) CleanupReceipt() processharness.CleanupReceipt {
	dashboard.cleanupMu.Lock()
	defer dashboard.cleanupMu.Unlock()
	receipt := dashboard.cleanupReceipt
	receipt.Processes = append([]processharness.CleanupRecord(nil), receipt.Processes...)
	return receipt
}

func (dashboard *Dashboard) WaitForGenerationAfter(ctx context.Context, generation uint64) error {
	if dashboard.receiptEvents == nil {
		return errors.New("receipt gate is disabled")
	}
	for {
		dashboard.eventMu.RLock()
		notify, closed := dashboard.eventNotify, dashboard.eventClosed
		dashboard.eventMu.RUnlock()
		dashboard.receiptMu.RLock()
		observed := dashboard.receiptGeneration > generation
		dashboard.receiptMu.RUnlock()
		if observed {
			return nil
		}
		if closed {
			return ErrReceiptGateClosed
		}
		select {
		case <-notify:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
