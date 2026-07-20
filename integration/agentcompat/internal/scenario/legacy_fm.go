//go:build linux

package scenario

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

type LegacyFMInput struct {
	Paths contract.Paths
	Fault contract.Fault
}

type LegacyFM struct{}

func (LegacyFM) Run(ctx context.Context, input LegacyFMInput) (result Result, runErr error) {
	assertions := NewAssertionSet()
	runID, err := newLegacyFMRunID()
	if err != nil {
		return Result{Name: "legacy-fm", Assertions: assertions.Results(), Error: err.Error()}, err
	}
	dashboardInstance, err := dashboard.Start(ctx, dashboard.StartConfig{SourceDir: input.Paths.NezhaSource().String(), ReceiptGate: true})
	if err != nil {
		return Result{Name: "legacy-fm", Assertions: assertions.Results(), Error: err.Error()}, err
	}
	result.CleanupOK = true
	defer func() {
		cleanupErr := dashboardInstance.Stop(context.Background())
		result.CleanupOK = result.CleanupOK && cleanupErr == nil && dashboardInstance.CleanupReceipt().Passed
		if cleanupErr != nil && runErr == nil {
			runErr = cleanupErr
		}
		result.Passed = runErr == nil
		result.Error = errorText(runErr)
	}()

	secret := dashboardInstance.AgentSecret()
	agentInstance, err := agent.Start(ctx, agent.AgentStartConfig{
		SourceDir: input.Paths.AgentSource().String(), Endpoint: dashboardInstance.Endpoint(),
		Secret: secret, UUID: "00000000-0000-0000-0000-000000000115",
		FMObserverRunID: runID,
	})
	if err != nil {
		return Result{Name: "legacy-fm", Assertions: assertions.Results(), CleanupOK: true, Error: err.Error()}, err
	}
	defer func() {
		cleanupErr := agentInstance.Stop(context.Background())
		result.CleanupOK = result.CleanupOK && cleanupErr == nil && agentInstance.CleanupReceipt().Passed
		if cleanupErr != nil && runErr == nil {
			runErr = cleanupErr
		}
	}()

	if input.Fault.String() == "agent-bad-secret" {
		return finishLegacyFM(assertions, errors.New("fault injection agent-bad-secret"))
	}
	if err := dashboardInstance.WaitForReceiptAccepted(ctx); err != nil {
		return finishLegacyFM(assertions, err)
	}
	if err := dashboardInstance.ReleaseReceipt(ctx); err != nil {
		return finishLegacyFM(assertions, err)
	}
	if _, err := agentInstance.WaitReady(ctx, dashboardInstance); err != nil {
		return finishLegacyFM(assertions, err)
	}
	serverID, err := findLegacyFMServerID(ctx, dashboardInstance, agentInstance.UUID())
	if err != nil {
		return finishLegacyFM(assertions, err)
	}

	root, err := fixture.NewAgentRoot(agentInstance.WorkspaceRoot(), "fm-files")
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	listPath, err := root.Path("legacy/list")
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	uploadPath, err := root.Path("legacy/upload.bin")
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	downloadPath, err := root.Path("legacy/download.bin")
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	payloadPattern := []byte("legacy-fm-exact-payload\x00\xff")
	payload := bytes.Repeat(payloadPattern, (1<<20+257)/len(payloadPattern)+1)
	payload = payload[:1<<20+257]
	sentinel := []byte("outside-fm-root-sentinel")
	sentinelPaths := []string{
		filepath.Join(agentInstance.WorkspaceRoot(), "outside-fm-root-a.txt"),
		filepath.Join(agentInstance.WorkspaceRoot(), "outside-fm-root-b.txt"),
	}
	for _, path := range sentinelPaths {
		if err := os.WriteFile(path, sentinel, 0o600); err != nil {
			return finishLegacyFM(assertions, err)
		}
	}
	if err := os.WriteFile(downloadPath.String(), payload, 0o600); err != nil {
		return finishLegacyFM(assertions, err)
	}
	if err := os.Mkdir(listPath.String(), 0o700); err != nil {
		return finishLegacyFM(assertions, err)
	}
	if err := os.WriteFile(filepath.Join(listPath.String(), "entry.txt"), []byte("entry"), 0o600); err != nil {
		return finishLegacyFM(assertions, err)
	}
	pathRejectionDispatches, err := probeLegacyFMRejectedPathDispatches(ctx, root)
	assertions.Record("rejected paths dispatch zero FM frames", err == nil && pathRejectionDispatches == 0, fmt.Sprintf("path_rejections_dispatched: %d", pathRejectionDispatches))
	if err != nil || pathRejectionDispatches != 0 {
		return finishLegacyFM(assertions, errors.Join(err, errors.New("rejected FM path dispatched a frame")))
	}
	baselineSample, err := processharness.SampleProcess(agentInstance.PID())
	if err != nil {
		return finishLegacyFM(assertions, err)
	}

	admin := dashboardInstance.Clients().REST
	session, err := createLegacyFMSession(ctx, admin, serverID)
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	ws, err := dashboardInstance.Clients().WebSocket.DialWebSocket(ctx, "/api/v1/ws/file/"+session)
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	defer ws.Close()

	dispatcher := legacyFMCommandDispatcher{writer: ws, root: root}
	if err := dispatcher.list(ctx, "legacy/list"); err != nil {
		return finishLegacyFM(assertions, err)
	}
	listFrame, err := readBinaryFrame(ctx, ws)
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	parsedList, err := parseLegacyFMList(listFrame)
	assertions.Record("list uses NZFN and exact entry", err == nil && parsedList.Path == listPath.String() && len(parsedList.Entries) == 1 && parsedList.Entries[0].Name == "entry.txt" && !parsedList.Entries[0].Dir, errorText(err))
	if err != nil {
		return finishLegacyFM(assertions, err)
	}

	if err := dispatcher.upload(ctx, "legacy/upload.bin", uint64(len(payload))); err != nil {
		return finishLegacyFM(assertions, err)
	}
	if err := ws.WriteFrame(ctx, client.Frame{Type: client.FrameBinary, Payload: payload[:1<<20]}); err != nil {
		return finishLegacyFM(assertions, err)
	}
	if err := ws.WriteFrame(ctx, client.Frame{Type: client.FrameBinary, Payload: payload[1<<20:]}); err != nil {
		return finishLegacyFM(assertions, err)
	}
	completion, err := readBinaryFrame(ctx, ws)
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	if err := requireLegacyFMMarker(completion, "NZUP"); err != nil {
		return finishLegacyFM(assertions, err)
	}
	written, err := os.ReadFile(uploadPath.String())
	assertions.Record("upload returns NZUP and exact bytes", err == nil && bytes.Equal(written, payload), errorText(err))
	if err != nil || !bytes.Equal(written, payload) {
		return finishLegacyFM(assertions, errors.New("uploaded content mismatch"))
	}

	if err := dispatcher.download(ctx, "legacy/download.bin"); err != nil {
		return finishLegacyFM(assertions, err)
	}
	producerAwaiter := newLegacyFMProducerAwaiter(agentInstance.FMProducerObserver(), runID, agentInstance.UUID(), session)
	activeProducerSample, err := producerAwaiter.await(ctx, "active")
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	header, err := readBinaryFrame(ctx, ws)
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	downloadHeader, err := parseLegacyFMDownload(header)
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	if downloadHeader.Size != uint64(len(payload)) {
		return finishLegacyFM(assertions, errors.New("download header size mismatch"))
	}
	downloaded, downloadFrameCount, err := readLegacyFMDownload(ctx, ws, downloadHeader.Size)
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	digest := sha256.Sum256(downloaded)
	wantDigest := sha256.Sum256(payload)
	assertions.Record("download returns NZTD across frames with exact hash/content", downloadFrameCount >= 2 && bytes.Equal(downloaded, payload) && digest == wantDigest, fmt.Sprintf("size=%d frames=%d", downloadHeader.Size, downloadFrameCount))
	if downloadFrameCount < 2 || !bytes.Equal(downloaded, payload) {
		return finishLegacyFM(assertions, errors.New("download framing or content mismatch"))
	}
	if err := dispatcher.download(ctx, "legacy/missing.bin"); err != nil {
		return finishLegacyFM(assertions, err)
	}
	errorFrame, err := readBinaryFrame(ctx, ws)
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	_, nerr := parseLegacyFMDownload(errorFrame)
	assertions.Record("missing download returns NERR", errors.Is(nerr, errLegacyFMRemote), errorText(nerr))

	scopeChecksErr := verifyLegacyFMMissingScopes(ctx, dashboardInstance, serverID, session)
	assertions.Record("each missing scope rejects FM creation and WebSocket", scopeChecksErr == nil, errorText(scopeChecksErr))
	if scopeChecksErr != nil {
		return finishLegacyFM(assertions, scopeChecksErr)
	}

	foreign, removeForeignUser, err := createForeignLegacyFMClient(ctx, dashboardInstance)
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	// Temporary users are scenario resources, so deletion failures must fail cleanup evidence.
	defer func() {
		cleanupErr := removeForeignUser()
		result.CleanupOK = result.CleanupOK && cleanupErr == nil
		if cleanupErr != nil && runErr == nil {
			runErr = cleanupErr
		}
	}()
	_, hijackErr := foreign.DialWebSocket(ctx, "/api/v1/ws/file/"+session)
	assertions.Record("foreign PAT cannot hijack FM session", isLegacyFMSessionRejected(hijackErr), errorText(hijackErr))

	if err := ws.Close(); err != nil {
		return finishLegacyFM(assertions, err)
	}
	closedProducerSample, err := producerAwaiter.await(ctx, "closed")
	if err != nil {
		return finishLegacyFM(assertions, err)
	}
	residueProbe := legacyFMResidueProbe{
		assertions: assertions, agentPID: agentInstance.PID(),
		session: session, root: root, baseline: baselineSample, sessionClient: dashboardInstance.Clients().WebSocket,
		producer: producerAwaiter.observation(activeProducerSample, closedProducerSample),
	}
	if err := residueProbe.run(ctx); err != nil {
		return finishLegacyFM(assertions, err)
	}

	sentinelErr := verifyLegacyFMSentinels(sentinelPaths, sentinel)
	assertions.Record("outside-root sentinels remain unchanged", sentinelErr == nil, errorText(sentinelErr))
	if sentinelErr != nil {
		return finishLegacyFM(assertions, sentinelErr)
	}
	filesystem := newMCPFilesystemClient(dashboardInstance.Clients().MCP, serverID, root)
	fixtureCleanupErr := cleanupLegacyFMFixtures(ctx, filesystem)
	assertions.Record("MCP cleanup removes FM fixture residue", fixtureCleanupErr == nil, errorText(fixtureCleanupErr))
	if fixtureCleanupErr != nil {
		return finishLegacyFM(assertions, fixtureCleanupErr)
	}
	return finishLegacyFM(assertions, nil)
}
