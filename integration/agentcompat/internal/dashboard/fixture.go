//go:build linux

package dashboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
)

func (dashboard *Dashboard) prepareFixture(ctx context.Context, config StartConfig) error {
	fileConfig, err := dashboard.prepareListeners(config)
	if err != nil {
		return err
	}
	dashboard.configPath = filepath.Join(dashboard.workspace.Root(), "dashboard.yaml")
	if err := writeDashboardConfig(dashboard.configPath, fileConfig); err != nil {
		return err
	}
	dashboard.databasePath = filepath.Join(dashboard.workspace.Root(), "dashboard.sqlite")
	dashboard.binaryPath, err = dashboard.workspace.Build(ctx, workspace.BuildSpec{Name: "dashboard", SourceDir: config.SourceDir, Package: "./cmd/dashboard", Tags: []string{"agentcompat"}})
	if err != nil {
		return err
	}
	return nil
}

func (dashboard *Dashboard) prepareListeners(config StartConfig) (dashboardConfig, error) {
	httpListener, err := dashboard.adoptLoopbackListener()
	if err != nil {
		return dashboardConfig{}, err
	}
	dashboard.httpAddress = httpListener.Address()
	dashboard.httpListener = httpListener
	fileConfig := dashboardConfig{HTTPAddress: dashboard.httpAddress}
	if config.ReceiptGate {
		receiptListener, err := dashboard.adoptLoopbackListener()
		if err != nil {
			return dashboardConfig{}, err
		}
		dashboard.receiptAddress = receiptListener.Address()
		dashboard.receiptListener = receiptListener
	}
	if config.EnableTLS {
		if _, err := dashboard.prepareTLSListener(&fileConfig); err != nil {
			return dashboardConfig{}, err
		}
	}
	return fileConfig, nil
}

func (dashboard *Dashboard) prepareTLSListener(config *dashboardConfig) (*os.File, error) {
	tlsFixture, err := fixture.NewLocalTLSFixture(time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("generate dashboard TLS fixture: %w", err)
	}
	dashboard.tlsFixture = tlsFixture
	config.CertificatePath = filepath.Join(dashboard.workspace.Root(), "dashboard.crt")
	config.KeyPath = filepath.Join(dashboard.workspace.Root(), "dashboard.key")
	if err := os.WriteFile(config.CertificatePath, tlsFixture.CertificatePEM(), 0o600); err != nil {
		return nil, fmt.Errorf("write dashboard certificate: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dashboard.workspace.Root(), "dashboard-ca.crt"), tlsFixture.CAPEM(), 0o600); err != nil {
		return nil, fmt.Errorf("write dashboard CA certificate: %w", err)
	}
	if err := os.WriteFile(config.KeyPath, tlsFixture.PrivateKeyPEM(), 0o600); err != nil {
		return nil, fmt.Errorf("write dashboard private key: %w", err)
	}
	listener, err := dashboard.adoptLoopbackListener()
	if err != nil {
		return nil, err
	}
	dashboard.httpsAddress = listener.Address()
	dashboard.httpsListener = listener
	config.HTTPSAddress = dashboard.httpsAddress
	return nil, nil
}
