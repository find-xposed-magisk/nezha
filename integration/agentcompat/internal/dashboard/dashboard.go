//go:build linux

package dashboard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
)

const (
	agentSecret             = "0123456789abcdef0123456789abcdef"
	jwtSecret               = "agentcompat-dashboard-jwt-secret"
	defaultReadinessTimeout = 60 * time.Second
	// Active Agent streams need the Dashboard graceful shutdown window before
	// the harness is allowed to escalate process-group cleanup to SIGKILL.
	defaultProcessStopTimeout   = 15 * time.Second
	defaultProcessKillTimeout   = 5 * time.Second
	failedStartCleanupTimeout   = 15 * time.Second
	dashboardMaxLogBytes        = 1 << 20
	dashboardHTTPClientTimeout  = 5 * time.Second
	dashboardRequestRetryPeriod = 25 * time.Millisecond
)

type StartConfig struct {
	SourceDir        string
	EnableTLS        bool
	ReceiptGate      bool
	ReadinessTimeout time.Duration
}

type Clients struct {
	REST      *client.Client
	MCP       *client.Client
	WebSocket *client.Client
}

type FixtureIdentity struct {
	WorkspaceRoot string
	ConfigPath    string
	DatabasePath  string
	BinaryPath    string
	HTTP          workspace.ListenerIdentity
	Receipt       workspace.ListenerIdentity
	HTTPS         workspace.ListenerIdentity
}

type RuntimeIdentity struct {
	Generation     uint64
	PID            int
	ProcessGroupID int
}

type dashboardGeneration struct {
	supervisor    dashboardSupervisor
	identity      RuntimeIdentity
	record        processharness.CleanupRecord
	receiptConn   net.Conn
	httpTransport *http.Transport
	tlsTransport  *http.Transport
}

type BootstrapResult struct {
	LoginAuthenticated bool
	CSRFCookiePresent  bool
	PATID              uint64
	PATScopes          []string
	MCPProtocolVersion string
	MCPServerName      string
	MCPToolCount       int
	TLSAuthenticated   bool
}

type dashboardSupervisor interface {
	Start() error
	Stop(context.Context) error
	Exited() <-chan struct{}
	PID() int
	ProcessGroupID() int
	CleanupRecord() processharness.CleanupRecord
}

type Dashboard struct {
	workspace        *workspace.Workspace
	supervisor       dashboardSupervisor
	clients          Clients
	restHTTPClient   *http.Client
	httpTransport    *http.Transport
	tlsTransport     *http.Transport
	tlsFixture       fixture.LocalTLSFixture
	httpAddress      string
	httpsAddress     string
	receiptAddress   string
	receiptConn      net.Conn
	receiptReader    *bufio.Reader
	receiptEvents    chan string
	eventNotify      chan struct{}
	eventMu          sync.RWMutex
	eventClosed      bool
	configPath       string
	databasePath     string
	logPath          string
	bootstrap        BootstrapResult
	readinessTimeout time.Duration
	startConfig      StartConfig
	binaryPath       string
	generation       uint64
	currentProcess   *dashboardGeneration
	processes        []*dashboardGeneration
	httpListener     *workspace.OwnedListener
	receiptListener  *workspace.OwnedListener
	httpsListener    *workspace.OwnedListener

	cleanupOnce          sync.Once
	cleanupDone          chan struct{}
	cleanupMu            sync.Mutex
	cleanupError         error
	cleanupReceipt       processharness.CleanupReceipt
	receiptMu            sync.RWMutex
	receiptAccepted      bool
	receiptAcceptedCount uint64
	receiptGeneration    uint64
	info2Mu              sync.Mutex
	info2Events          map[string]struct{}
	stateMu              sync.Mutex
	stateEvents          map[stateEventIdentity]struct{}
	mcpReceiptEvents     []MCPReceiptEvent
	mcpReceiptSequence   uint64
	eventGeneration      uint64
	lifecycleMu          sync.Mutex
}

type stateEventIdentity struct {
	ServerID   uint64
	UUID       string
	Generation uint64
	Count      uint64
}

type MCPReceiptKind string

const (
	MCPReceiptTask   MCPReceiptKind = "task"
	MCPReceiptResult MCPReceiptKind = "result"
)

type MCPReceiptCursor struct {
	Sequence uint64
}

type MCPReceiptEvent struct {
	Sequence            uint64         `json:"sequence"`
	DashboardGeneration uint64         `json:"dashboard_generation"`
	GateGeneration      uint64         `json:"gate_generation"`
	ServerID            uint64         `json:"server_id"`
	TaskID              uint64         `json:"task_id"`
	TaskType            uint64         `json:"task_type"`
	Kind                MCPReceiptKind `json:"kind"`
}

type MCPReceiptExpectation struct {
	DashboardGeneration uint64
	GateGeneration      uint64
	ServerID            uint64
	TaskID              uint64
	TaskType            uint64
}

type MCPReceiptPair struct {
	Task   MCPReceiptEvent `json:"task"`
	Result MCPReceiptEvent `json:"result"`
}

var ErrReceiptGateClosed = errors.New("receipt gate closed")

func Start(ctx context.Context, config StartConfig) (*Dashboard, error) {
	if config.SourceDir == "" || !filepath.IsAbs(config.SourceDir) {
		return nil, errors.New("dashboard source directory must be absolute")
	}
	// Dashboard owns cancellation order so the process group is gone before the
	// workspace verifies listeners, PIDs, and temporary files are absent.
	workspaceRoot, err := workspace.New(context.WithoutCancel(ctx))
	if err != nil {
		return nil, fmt.Errorf("create dashboard workspace: %w", err)
	}
	dashboard := &Dashboard{workspace: workspaceRoot, cleanupDone: make(chan struct{}), startConfig: config}
	dashboard.readinessTimeout = config.ReadinessTimeout
	if dashboard.readinessTimeout <= 0 {
		dashboard.readinessTimeout = defaultReadinessTimeout
	}
	if err := dashboard.prepare(ctx, config); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), failedStartCleanupTimeout)
		defer cancel()
		return nil, errors.Join(err, dashboard.Stop(cleanupContext))
	}
	go dashboard.cleanupOnCancellation(ctx)
	return dashboard, nil
}

func (dashboard *Dashboard) Stop(ctx context.Context) error {
	dashboard.cleanupOnce.Do(func() { go dashboard.cleanup(context.WithoutCancel(ctx)) })
	select {
	case <-dashboard.cleanupDone:
		dashboard.cleanupMu.Lock()
		defer dashboard.cleanupMu.Unlock()
		return dashboard.cleanupError
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (dashboard *Dashboard) Close(ctx context.Context) error { return dashboard.Stop(ctx) }

func (dashboard *Dashboard) URL() string { return "http://" + dashboard.httpAddress }

func (dashboard *Dashboard) Endpoint() string { return dashboard.httpAddress }

func (dashboard *Dashboard) TLSEndpoint() string { return dashboard.httpsAddress }

func (dashboard *Dashboard) ReceiptGateEnabled() bool { return dashboard.receiptAddress != "" }

func (dashboard *Dashboard) ReceiptGateEndpoint() string { return dashboard.receiptAddress }
