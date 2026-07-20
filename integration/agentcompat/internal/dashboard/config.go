//go:build linux

package dashboard

import (
	"fmt"
	"net"
	"os"
)

type dashboardConfig struct {
	HTTPAddress     string
	HTTPSAddress    string
	ReceiptAddress  string
	CertificatePath string
	KeyPath         string
}

func writeDashboardConfig(path string, config dashboardConfig) error {
	httpHost, httpPort, err := splitAddress(config.HTTPAddress)
	if err != nil {
		return err
	}
	httpsPort := "0"
	if config.HTTPSAddress != "" {
		_, httpsPort, err = splitAddress(config.HTTPSAddress)
		if err != nil {
			return err
		}
	}
	content := fmt.Sprintf(`listen_host: %s
listen_port: %s
location: UTC
force_auth: true
agent_secret_key: %q
jwt_timeout: 1
enable_mcp: true
oauth2: {}
tsdb:
  data_path: ""
https:
  listen_port: %s
  tls_cert_path: %q
  tls_key_path: %q
  insecure_tls: false
`, httpHost, httpPort, agentSecret, httpsPort, config.CertificatePath, config.KeyPath)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write dashboard config: %w", err)
	}
	return nil
}

func splitAddress(address string) (string, string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "", fmt.Errorf("split loopback listener address %q: %w", address, err)
	}
	if host == "" || port == "" {
		return "", "", fmt.Errorf("split loopback listener address %q: host and port are required", address)
	}
	return host, port, nil
}
