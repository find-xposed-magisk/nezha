//go:build linux

package dashboard

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
)

func (dashboard *Dashboard) prepare(ctx context.Context, config StartConfig) error {
	if err := dashboard.prepareFixture(ctx, config); err != nil {
		return err
	}
	process, err := dashboard.startGeneration(ctx, 1)
	if err != nil {
		return err
	}
	dashboard.generation = 1
	dashboard.currentProcess, dashboard.supervisor = process, process.supervisor
	dashboard.processes = append(dashboard.processes, process)
	return nil
}

func (dashboard *Dashboard) startGeneration(ctx context.Context, generation uint64) (*dashboardGeneration, error) {
	files := make([]*os.File, 0, 3)
	for _, listener := range []*workspace.OwnedListener{dashboard.httpListener, dashboard.receiptListener, dashboard.httpsListener} {
		if listener == nil {
			continue
		}
		file, err := listener.ExtraFile()
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	logFile, err := dashboard.workspace.Log(fmt.Sprintf("dashboard-generation-%d", generation))
	if err != nil {
		return nil, err
	}
	dashboard.logPath = logFile.Name()
	supervisor := processharness.NewSupervisor(context.WithoutCancel(ctx), processharness.Spec{
		Name: "dashboard", Path: dashboard.binaryPath, Args: []string{"-c", dashboard.configPath, "-db", dashboard.databasePath},
		Env: dashboardEnvironment(dashboard.startConfig.EnableTLS, dashboard.startConfig.ReceiptGate), ExtraFiles: files,
		Stdout: logFile, Stderr: logFile, MaxLogBytes: dashboardMaxLogBytes,
		TerminateTimeout: defaultProcessStopTimeout, KillTimeout: defaultProcessKillTimeout,
	})
	if err := supervisor.Start(); err != nil {
		return nil, err
	}
	identity := RuntimeIdentity{Generation: generation, PID: supervisor.PID(), ProcessGroupID: supervisor.ProcessGroupID()}
	process := &dashboardGeneration{supervisor: supervisor, identity: identity}
	rollback := true
	defer func() {
		if rollback {
			_ = process.supervisor.Stop(context.WithoutCancel(ctx))
			if process.receiptConn != nil {
				_ = process.receiptConn.Close()
			}
		}
	}()
	if err := dashboard.workspace.TrackPID(identity.PID); err != nil {
		return nil, err
	}
	if err := dashboard.workspace.TrackProcessGroup(identity.ProcessGroupID); err != nil {
		return nil, err
	}
	dashboard.supervisor = supervisor
	dashboard.stateMu.Lock()
	dashboard.eventGeneration = generation
	dashboard.stateMu.Unlock()
	if dashboard.startConfig.ReceiptGate {
		connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", dashboard.receiptAddress)
		if err != nil {
			return nil, fmt.Errorf("connect dashboard receipt gate: %w", err)
		}
		process.receiptConn = connection
		reader := bufio.NewReader(connection)
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("wait for dashboard receipt gate: %w", err)
		}
		if line != "ready\n" {
			return nil, fmt.Errorf("unexpected dashboard receipt gate handshake %q", line)
		}
		dashboard.eventMu.Lock()
		dashboard.receiptConn = connection
		dashboard.receiptReader = reader
		dashboard.receiptEvents = make(chan string, 16)
		dashboard.eventNotify = make(chan struct{})
		dashboard.eventClosed = false
		dashboard.eventMu.Unlock()
		dashboard.info2Mu.Lock()
		dashboard.info2Events = make(map[string]struct{})
		dashboard.info2Mu.Unlock()
		dashboard.stateMu.Lock()
		dashboard.stateEvents = make(map[stateEventIdentity]struct{})
		dashboard.stateMu.Unlock()
		go dashboard.readReceiptEvents(generation, reader)
	}
	if err := dashboard.refreshClients(ctx); err != nil {
		return nil, err
	}
	process.httpTransport = dashboard.httpTransport
	process.tlsTransport = dashboard.tlsTransport
	if dashboard.startConfig.EnableTLS {
		if err := dashboard.verifyTrustedTLS(ctx); err != nil {
			return nil, err
		}
	}
	rollback = false
	return process, nil
}

func (dashboard *Dashboard) adoptLoopbackListener() (*workspace.OwnedListener, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for dashboard: %w", err)
	}
	owned, err := dashboard.workspace.AdoptListener(listener)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("adopt dashboard listener: %w", err)
	}
	return owned, nil
}

func (dashboard *Dashboard) refreshClients(ctx context.Context) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	dashboard.httpTransport = &http.Transport{DialContext: dialAddress(dashboard.httpAddress)}
	dashboard.restHTTPClient = &http.Client{Transport: dashboard.httpTransport, Jar: jar, Timeout: dashboardHTTPClientTimeout}
	dashboard.clients.REST, err = client.New(client.Config{BaseURL: dashboard.URL(), HTTPClient: dashboard.restHTTPClient})
	if err != nil {
		return err
	}
	pat, err := dashboard.bootstrapAuthentication(ctx)
	if err != nil {
		return err
	}
	return dashboard.initializeAuthenticatedClients(ctx, pat)
}

func dashboardEnvironment(enableTLS bool, receiptGateOption ...bool) []string {
	receiptGate := len(receiptGateOption) > 0 && receiptGateOption[0]
	environment := make([]string, 0, len(os.Environ())+3)
	for _, variable := range os.Environ() {
		if strings.HasPrefix(variable, "NZ_") || strings.HasPrefix(variable, "NEZHA_AGENTCOMPAT_") {
			continue
		}
		environment = append(environment, variable)
	}
	environment = append(environment,
		"NZ_JWTSECRETKEY="+jwtSecret,
		"NEZHA_AGENTCOMPAT_HTTP_LISTENER_FD=3",
	)
	if receiptGate {
		environment = append(environment, "NEZHA_AGENTCOMPAT_RECEIPT_LISTENER_FD=4")
	}
	if enableTLS {
		fd := 4
		if receiptGate {
			fd = 5
		}
		environment = append(environment, fmt.Sprintf("NEZHA_AGENTCOMPAT_HTTPS_LISTENER_FD=%d", fd))
	}
	return environment
}

func dialAddress(address string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, address)
	}
}
