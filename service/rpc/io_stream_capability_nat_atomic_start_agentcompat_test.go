//go:build agentcompat

package rpc

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type atomicNATEndpoint struct {
	handler   *NezhaHandler
	data      *bytes.Reader
	written   bytes.Buffer
	mu        sync.Mutex
	closed    atomic.Int32
	readSeen  atomic.Int32
	writeSeen chan struct{}
}

func (endpoint *atomicNATEndpoint) Read(data []byte) (int, error) {
	endpoint.readSeen.Add(1)
	endpoint.handler.SnapshotIOStreamState()
	return endpoint.data.Read(data)
}

func (endpoint *atomicNATEndpoint) Write(data []byte) (int, error) {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	endpoint.handler.SnapshotIOStreamState()
	n, err := endpoint.written.Write(data)
	if endpoint.writeSeen != nil {
		select {
		case <-endpoint.writeSeen:
		default:
			close(endpoint.writeSeen)
		}
	}
	return n, err
}

func (endpoint *atomicNATEndpoint) Close() error {
	endpoint.closed.Add(1)
	endpoint.handler.SnapshotIOStreamState()
	return nil
}

func newAtomicNATEndpoint(handler *NezhaHandler, payload string) *atomicNATEndpoint {
	return &atomicNATEndpoint{handler: handler, data: bytes.NewReader([]byte(payload)), writeSeen: make(chan struct{})}
}

func publishAtomicNATStream(t *testing.T, streamID string) (*NezhaHandler, AgentCompatCapabilityAccess, AgentCompatNATPublishHandle, AgentCompatCapabilityRegistration) {
	t.Helper()
	handler, registration, capability := natCapabilityFixture(t, 301, 302, 303, 304)
	access := capabilityAccess(capability, registration)
	handle, err := handler.ConsumeAgentCompatNATCapability(access)
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(handle, streamID)
	require.NoError(t, err)
	require.NoError(t, handler.PublishAgentCompatNATStream(handle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 303, ResourceID: 304, StreamID: streamID,
	}))
	return handler, access, handle, registration
}

func TestAgentCompatNATAtomicStartWhenCanceledBeforeCaptureDoesNotTouchReplacement(t *testing.T) {
	handler, access, handle, _ := publishAtomicNATStream(t, "atomic-replacement-before-capture")
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(access))
	require.NoError(t, handler.CreateStreamWithPurpose("atomic-replacement-before-capture", 0, 303, PurposeNAT))
	replacement := newAtomicNATEndpoint(handler, "replacement")
	require.NoError(t, handler.UserConnected("atomic-replacement-before-capture", replacement))
	require.NoError(t, handler.AgentConnected("atomic-replacement-before-capture", replacement))

	publicationOwned, err := handler.StartAgentCompatNATStream(handle, time.Millisecond)

	require.True(t, publicationOwned)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
	require.Equal(t, int32(0), replacement.readSeen.Load())
	require.Equal(t, int32(0), replacement.closed.Load())
	_, found := handler.StreamOwnership("atomic-replacement-before-capture")
	require.True(t, found)
	t.Logf("replacement after cancel-before-capture: read=%d write=%d close=%d registered=%t", replacement.readSeen.Load(), replacement.written.Len(), replacement.closed.Load(), found)
}

func TestAgentCompatNATAtomicStartWhenCanceledAfterCaptureDoesNotCloseReplacement(t *testing.T) {
	handler, access, handle, _ := publishAtomicNATStream(t, "atomic-replacement-after-capture")
	old := newAtomicNATEndpoint(handler, "old")
	require.NoError(t, handler.UserConnected("atomic-replacement-after-capture", old))

	result := make(chan error, 1)
	go func() {
		_, err := handler.StartAgentCompatNATStream(handle, time.Second)
		result <- err
	}()
	stream := mustGetStream(t, handler, "atomic-replacement-after-capture")
	select {
	case <-stream.startCaptureCh:
	case <-time.After(time.Second):
		t.Fatal("atomic start did not capture retained stream")
	}
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(access))
	require.NoError(t, handler.CreateStreamWithPurpose("atomic-replacement-after-capture", 0, 303, PurposeNAT))
	replacement := newAtomicNATEndpoint(handler, "replacement")
	require.NoError(t, handler.UserConnected("atomic-replacement-after-capture", replacement))
	require.NoError(t, handler.AgentConnected("atomic-replacement-after-capture", replacement))

	require.EqualError(t, receiveAtomicNATError(t, result), "stream revoked")
	require.Equal(t, int32(1), old.closed.Load())
	require.Equal(t, int32(0), replacement.readSeen.Load())
	require.Equal(t, int32(0), replacement.closed.Load())
	_, found := handler.StreamOwnership("atomic-replacement-after-capture")
	require.True(t, found)
	t.Logf("replacement after cancel-after-capture: read=%d write=%d close=%d registered=%t", replacement.readSeen.Load(), replacement.written.Len(), replacement.closed.Load(), found)
}

func TestAgentCompatNATAtomicStartDetachesOnlyRetainedStreamAfterRelay(t *testing.T) {
	handler, _, handle, registration := publishAtomicNATStream(t, "atomic-normal-completion")
	user := newAtomicNATEndpoint(handler, "request-bytes")
	agent := newAtomicNATEndpoint(handler, "")
	require.NoError(t, handler.UserConnected("atomic-normal-completion", user))
	require.NoError(t, handler.AgentConnected("atomic-normal-completion", agent))

	result := make(chan error, 1)
	var publicationOwned bool
	go func() {
		var err error
		publicationOwned, err = handler.StartAgentCompatNATStream(handle, time.Second)
		result <- err
	}()
	select {
	case <-agent.writeSeen:
	case <-time.After(time.Second):
		t.Fatal("atomic relay did not transfer request bytes")
	}
	err := receiveAtomicNATError(t, result)
	require.True(t, publicationOwned)
	require.NoError(t, err)
	require.Equal(t, int32(1), user.closed.Load())
	require.Equal(t, int32(1), agent.closed.Load())
	require.Equal(t, "request-bytes", agent.written.String())
	streamID, waitErr := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccessFromRegistration(handle, registration))
	require.NoError(t, waitErr)
	require.Equal(t, "atomic-normal-completion", streamID)
	require.NoError(t, handler.CreateStreamWithPurpose("atomic-normal-completion", 0, 303, PurposeNAT))
	replacement := newAtomicNATEndpoint(handler, "replacement")
	require.NoError(t, handler.UserConnected("atomic-normal-completion", replacement))
	require.NoError(t, handler.AgentConnected("atomic-normal-completion", replacement))
	require.Equal(t, int32(0), replacement.readSeen.Load())
	require.Equal(t, int32(0), replacement.closed.Load())
	t.Logf("replacement after normal retained teardown: read=%d write=%d close=%d registered=true", replacement.readSeen.Load(), replacement.written.Len(), replacement.closed.Load())
}

func capabilityAccessFromRegistration(handle AgentCompatNATPublishHandle, registration AgentCompatCapabilityRegistration) AgentCompatCapabilityAccess {
	return AgentCompatCapabilityAccess{Capability: AgentCompatIOStreamCapability{value: handle.capability}, Owner: registration.Owner, Purpose: registration.Purpose, TargetServerID: registration.TargetServerID, ResourceID: registration.ResourceID, ServerAccessAllowed: registration.ServerAccessAllowed}
}

func mustGetStream(t *testing.T, handler *NezhaHandler, streamID string) *ioStreamContext {
	t.Helper()
	stream, err := handler.GetStream(streamID)
	require.NoError(t, err)
	return stream
}

func receiveAtomicNATError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("atomic start did not return")
		return nil
	}
}

var _ io.ReadWriteCloser = (*atomicNATEndpoint)(nil)
