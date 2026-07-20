//go:build linux

package scenario

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

func TestNewHeldLegacyFMSessionRejectsInvalidTypedInputs(t *testing.T) {
	// Given
	plan := heldFMTestPlan(t, StressSessionFM)
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000305")
	readiness := completeHeldReadiness(agentInstance.UUID())
	valid := heldLegacyFMInput{Dashboard: &dashboard.Dashboard{}, PATClient: &client.Client{}, Agent: agentInstance, Readiness: readiness, Plan: plan}
	cases := []struct {
		name  string
		input heldLegacyFMInput
	}{
		{name: "nil dashboard", input: heldLegacyFMInput{Agent: valid.Agent, Readiness: readiness, Plan: plan}},
		{name: "nil agent", input: heldLegacyFMInput{Dashboard: valid.Dashboard, Readiness: readiness, Plan: plan}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// When
			_, err := newHeldLegacyFMSession(context.Background(), testCase.input)

			// Then
			require.ErrorIs(t, err, ErrInvalidHeldLegacyFMInput)
		})
	}
}

func TestNewHeldLegacyFMSessionReturnsPreciseReadinessErrorForZeroServerID(t *testing.T) {
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000306")
	input := heldLegacyFMInput{Dashboard: &dashboard.Dashboard{}, PATClient: &client.Client{}, Agent: agentInstance, Readiness: completeHeldReadiness(agentInstance.UUID()), Plan: heldFMTestPlan(t, StressSessionFM)}
	input.Readiness.ServerID = 0

	_, err := newHeldLegacyFMSession(context.Background(), input)

	require.ErrorIs(t, err, ErrHeldReadinessServerID)
	require.NotErrorIs(t, err, ErrInvalidHeldLegacyFMInput)
}

func TestNewHeldLegacyFMSessionReturnsPreciseReadinessErrorForUUIDMismatch(t *testing.T) {
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000307")
	input := heldLegacyFMInput{Dashboard: &dashboard.Dashboard{}, PATClient: &client.Client{}, Agent: agentInstance, Readiness: completeHeldReadiness("00000000-0000-0000-0000-000000000308"), Plan: heldFMTestPlan(t, StressSessionFM)}

	_, err := newHeldLegacyFMSession(context.Background(), input)

	require.ErrorIs(t, err, ErrHeldReadinessAgentMismatch)
	require.NotErrorIs(t, err, ErrInvalidHeldLegacyFMInput)
}

func TestHeldLegacyFMSessionImplementsHeldSession(t *testing.T) {
	var _ heldSession = (*heldLegacyFMSession)(nil)
}

func TestNewHeldLegacyFMSessionRejectsNilContext(t *testing.T) {
	// Given
	input := heldLegacyFMInput{
		Dashboard: &dashboard.Dashboard{}, PATClient: &client.Client{}, Agent: &agent.Agent{},
		Readiness: agent.Readiness{ServerID: 9, UUID: "agent-uuid", Online: true}, Plan: heldFMTestPlan(t, StressSessionFM),
	}

	// When
	_, err := newHeldLegacyFMSession(nil, input)

	// Then
	require.ErrorIs(t, err, ErrInvalidHeldLegacyFMInput)
}

func TestNewHeldLegacyFMSessionRejectsNilPATBeforeMutation(t *testing.T) {
	// Given
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000303")
	input := heldLegacyFMInput{
		Dashboard: &dashboard.Dashboard{},
		PATClient: nil,
		Agent:     agentInstance,
		Readiness: completeHeldReadiness(agentInstance.UUID()),
		Plan:      heldFMTestPlan(t, StressSessionFM),
	}
	workspaceRoot := agentInstance.WorkspaceRoot()
	before, err := os.ReadDir(workspaceRoot)
	require.NoError(t, err)

	// When
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, err = newHeldLegacyFMSession(context.Background(), input)
	}()

	// Then
	require.Nil(t, recovered)
	require.ErrorIs(t, err, ErrInvalidHeldPATClient)
	after, readErr := os.ReadDir(workspaceRoot)
	require.NoError(t, readErr)
	require.Equal(t, before, after)
	_, statErr := os.Stat(filepath.Join(workspaceRoot, "held-fm-held-fm-session"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestHeldLegacyFMSessionRejectsNonFMPlanIdentity(t *testing.T) {
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000309")
	plan := heldFMTestPlan(t, StressSessionTerminal)
	input := heldLegacyFMInput{
		Dashboard: &dashboard.Dashboard{}, PATClient: &client.Client{}, Agent: agentInstance,
		Readiness: completeHeldReadiness(agentInstance.UUID()), Plan: plan,
	}

	// When
	_, err := newHeldLegacyFMSession(context.Background(), input)

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidHeldLegacyFMInput)
}

func TestHeldLegacyFMInputRejectsPATClientBeforeOtherValidation(t *testing.T) {
	// Given
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000304")
	input := heldLegacyFMInput{
		Dashboard: &dashboard.Dashboard{},
		Agent:     agentInstance,
		Readiness: completeHeldReadiness(agentInstance.UUID()),
		Plan:      heldFMTestPlan(t, StressSessionFM),
	}

	// When
	err := validateHeldLegacyFMInput(context.Background(), input)

	// Then
	require.ErrorIs(t, err, ErrInvalidHeldPATClient)
}

func TestHeldLegacyFMStreamMismatchErrorDoesNotExposeIdentifiers(t *testing.T) {
	// Given
	responseID := "response-secret-session"
	capabilityID := "capability-secret-stream"

	// When
	err := heldLegacyFMStreamMismatchError()
	message := err.Error()

	// Then
	require.ErrorIs(t, err, ErrHeldLegacyFMProtocol)
	require.NotContains(t, message, responseID)
	require.NotContains(t, message, capabilityID)
	require.NotContains(t, message, "Authorization")
}

func TestHeldLegacyFMListProofRequiresBinaryExactNZFNPathAndEntry(t *testing.T) {
	// Given
	root := "/workspace/held-fm/list"
	valid := heldLegacyFMListFrame(root, "entry.txt", false)
	cases := []struct {
		name  string
		frame client.Frame
	}{
		{name: "valid", frame: client.Frame{Type: client.FrameBinary, Payload: valid}},
		{name: "text", frame: client.Frame{Type: client.FrameText, Payload: valid}},
		{name: "wrong path", frame: client.Frame{Type: client.FrameBinary, Payload: heldLegacyFMListFrame("/other", "entry.txt", false)}},
		{name: "directory entry", frame: client.Frame{Type: client.FrameBinary, Payload: heldLegacyFMListFrame(root, "entry.txt", true)}},
		{name: "remote error", frame: client.Frame{Type: client.FrameBinary, Payload: []byte("NERRdenied")}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			release := make(chan struct{})
			server := heldPumpServer(t, func(connection *websocket.Conn) {
				messageType := websocket.BinaryMessage
				if testCase.frame.Type == client.FrameText {
					messageType = websocket.TextMessage
				}
				require.NoError(t, connection.WriteMessage(messageType, testCase.frame.Payload))
				<-release
			})
			connection := heldPumpConnection(t, server)
			pump, err := newHeldWebSocketPump(context.Background(), connection, 1)
			require.NoError(t, err)
			err = proveHeldLegacyFMList(context.Background(), pump, root)
			if testCase.name == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), root)
				require.NotContains(t, err.Error(), "entry.txt")
			}
			require.NoError(t, pump.Stop(context.Background()))
			close(release)
		})
	}
}

func TestHeldLegacyFMCanceledCloseWaiterRetainsCleanupResult(t *testing.T) {
	// Given
	lifecycle, err := newHeldSessionLifecycle(context.Background(), heldFMTestPlan(t, StressSessionFM), "held-fm-stream", time.Second)
	require.NoError(t, err)
	require.NoError(t, lifecycle.markLive(nil))
	started := make(chan struct{})
	release := make(chan struct{})
	stack := newHeldCleanupStack()
	require.NoError(t, stack.Push(heldCleanupAction{name: "blocked cleanup", cleanup: func(context.Context) error {
		close(started)
		<-release
		return nil
	}}))
	session := &heldLegacyFMSession{lifecycle: lifecycle, stack: stack}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	first := make(chan error, 1)
	go func() { first <- session.Close(canceled) }()
	<-started

	// Then
	require.ErrorIs(t, <-first, context.Canceled)
	close(release)
	require.NoError(t, session.Close(context.Background()))
}

func heldLegacyFMListFrame(path, name string, directory bool) []byte {
	kind := byte(0)
	if directory {
		kind = 1
	}
	frame := make([]byte, 8, 8+len(path)+2+len(name))
	copy(frame, []byte("NZFN"))
	binary.BigEndian.PutUint32(frame[4:], uint32(len(path)))
	frame = append(frame, []byte(path)...)
	frame = append(frame, kind, byte(len(name)))
	return append(frame, []byte(name)...)
}

func heldFMTestPlan(t *testing.T, kind StressSessionKind) StressSessionPlan {
	t.Helper()
	id, err := NewStressSessionID("held-fm-session")
	require.NoError(t, err)
	ordinal, err := NewStressAgentOrdinal(1)
	require.NoError(t, err)
	return StressSessionPlan{ID: id, Kind: kind, Ordinal: 1, Agent: ordinal}
}
