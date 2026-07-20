//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
)

func agentBuildSpec(sourceDir string) workspace.BuildSpec {
	return workspace.BuildSpec{Name: "agent", SourceDir: sourceDir, Package: "./cmd/agent", Tags: []string{"agentcompat"}, Ldflags: []string{"-X", "github.com/nezhahq/agent/pkg/monitor.Version=v2.1.0"}}
}

func (agent *Agent) prepareFixture(ctx context.Context, config AgentStartConfig) error {
	if err := agent.prepareConfig(config); err != nil {
		return err
	}
	if err := agent.prepareFMObserver(config); err != nil {
		return err
	}
	if err := agent.prepareBinary(ctx, config); err != nil {
		return err
	}
	if err := agent.grantWorkspaceOwnership(config); err != nil {
		return err
	}
	agent.prepareEnvironment(config)
	return nil
}

func (agent *Agent) prepareConfig(config AgentStartConfig) error {
	configPath, err := agent.workspace.PayloadPath("config.yml")
	if err != nil {
		return err
	}
	agent.configPath = configPath
	content := fmt.Sprintf("server: %q\nclient_secret: %q\nuuid: %q\ndisable_auto_update: true\ndisable_command_execute: false\ndisable_nat: false\nreport_delay: 1\nip_report_period: 30\nskip_connection_count: %t\ntls: %t\ninsecure_tls: false\ndebug: %t\n", config.Endpoint, config.Secret, config.UUID, config.SkipConnectionCount, config.TLS, config.Debug)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write agent config: %w", err)
	}
	if !config.TLS || config.CAFilePath == "" {
		return nil
	}
	ca, err := os.ReadFile(config.CAFilePath)
	if err != nil {
		return fmt.Errorf("read agent CA certificate: %w", err)
	}
	caPath, err := agent.workspace.PayloadPath("agent-ca.crt")
	if err != nil {
		return err
	}
	if err := os.WriteFile(caPath, ca, 0o600); err != nil {
		return fmt.Errorf("write agent CA certificate: %w", err)
	}
	agent.caFilePath = caPath
	return nil
}

func (agent *Agent) prepareFMObserver(config AgentStartConfig) error {
	if config.FMObserverRunID == "" {
		return nil
	}
	agent.fmObserverPath = fmObserverSocketPath(agent.workspace.Root())
	observer, err := newFMProducerObserver(agent.fmObserverPath)
	if err != nil {
		return err
	}
	agent.fmObserver = observer
	return nil
}

func (agent *Agent) prepareBinary(ctx context.Context, config AgentStartConfig) error {
	if config.PreparedBinary != nil {
		binaryPath, release, err := config.PreparedBinary.acquire()
		if err != nil {
			return err
		}
		agent.binaryPath = binaryPath
		agent.releaseBinary = release
		agent.releasePending = true
		return nil
	}
	binaryPath, err := agent.workspace.Build(ctx, agentBuildSpec(config.SourceDir))
	if err != nil {
		return err
	}
	agent.binaryPath = binaryPath
	return nil
}

func (agent *Agent) grantWorkspaceOwnership(config AgentStartConfig) error {
	if config.Credential == nil {
		return nil
	}
	if err := filepath.WalkDir(agent.workspace.Root(), func(path string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return os.Chown(path, int(config.Credential.Uid), int(config.Credential.Gid))
	}); err != nil {
		return fmt.Errorf("grant agent workspace ownership: %w", err)
	}
	return nil
}

func (agent *Agent) prepareEnvironment(config AgentStartConfig) {
	environment := filteredEnvironment()
	if config.FMObserverRunID != "" {
		environment = append(environment, "AGENTCOMPAT_FM_OBSERVER_SOCKET="+agent.fmObserverPath, "AGENTCOMPAT_FM_OBSERVER_RUN_ID="+config.FMObserverRunID)
	}
	if config.TLS && agent.caFilePath != "" {
		environment = append(environment, "SSL_CERT_FILE="+agent.caFilePath)
	}
	agent.environment = append([]string(nil), environment...)
}
