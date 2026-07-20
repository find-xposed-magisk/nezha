//go:build linux

package scenario

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

type legacyFMProducerObservation struct {
	RunID     string
	AgentUUID string
	SessionID string
	Samples   []agent.FMProducerSample
}

type legacyFMProducerAwaiter struct {
	observer *agent.FMProducerObserver
	identity legacyFMProducerObservation
}

func newLegacyFMProducerAwaiter(observer *agent.FMProducerObserver, runID, agentUUID, sessionID string) legacyFMProducerAwaiter {
	return legacyFMProducerAwaiter{
		observer: observer,
		identity: legacyFMProducerObservation{RunID: runID, AgentUUID: agentUUID, SessionID: sessionID},
	}
}

func (awaiter legacyFMProducerAwaiter) observation(active, closed agent.FMProducerSample) legacyFMProducerObservation {
	result := awaiter.identity
	result.Samples = []agent.FMProducerSample{active, closed}
	return result
}

func (awaiter legacyFMProducerAwaiter) await(ctx context.Context, phase string) (agent.FMProducerSample, error) {
	return awaiter.observer.Await(ctx, func(sample agent.FMProducerSample) bool {
		matches := sample.RunID == awaiter.identity.RunID && sample.AgentUUID == awaiter.identity.AgentUUID && sample.SessionID == awaiter.identity.SessionID && sample.Phase == phase
		return matches && (phase != "active" || sample.Active > 0)
	})
}

func (observation legacyFMProducerObservation) validate() error {
	if observation.RunID == "" || observation.AgentUUID == "" || observation.SessionID == "" {
		return errors.New("FM producer observation identity is incomplete")
	}
	activeObserved := false
	closedObserved := false
	for _, sample := range observation.Samples {
		if sample.RunID != observation.RunID || sample.AgentUUID != observation.AgentUUID || sample.SessionID != observation.SessionID {
			return errors.New("FM producer observation identity mismatch")
		}
		switch sample.Phase {
		case "active":
			activeObserved = activeObserved || sample.Active > 0
		case "idle":
			continue
		case "closed":
			closedObserved = sample.Active == 0
		}
	}
	if !activeObserved {
		return errors.New("FM producer observation never saw an active producer")
	}
	if !closedObserved {
		return errors.New("FM producer observation omitted closed zero state")
	}
	return nil
}

func (observation legacyFMProducerObservation) details() string {
	active := int64(0)
	closed := int64(-1)
	for _, sample := range observation.Samples {
		if sample.Phase == "active" && sample.Active > active {
			active = sample.Active
		}
		if sample.Phase == "closed" {
			closed = sample.Active
		}
	}
	return fmt.Sprintf("fm_producer_active_count: %d; fm_producer_residue_count: %d; run_id=%s agent_uuid=%s session_id=%s source=live-agent-task", active, closed, observation.RunID, observation.AgentUUID, observation.SessionID)
}

type legacyFMSentinelCoverage struct {
	ListRejected     bool
	UploadRejected   bool
	DownloadRejected bool
	SuccessCount     int
	ErrorCount       int
}

func (coverage legacyFMSentinelCoverage) validate() error {
	if !coverage.ListRejected || !coverage.UploadRejected || !coverage.DownloadRejected {
		return errors.New("sentinel coverage omitted a typed rejected dispatcher")
	}
	if coverage.SuccessCount == 0 || coverage.ErrorCount == 0 {
		return errors.New("sentinel coverage omitted a real success or error operation")
	}
	return nil
}

type legacyFMProcessResidue struct {
	Baseline processharness.Sample
	End      processharness.Sample
}

func (residue legacyFMProcessResidue) validate() error {
	if residue.Baseline.NonStdioFDCount != residue.End.NonStdioFDCount {
		return fmt.Errorf("Agent non-stdio FD drift: baseline=%d end=%d", residue.Baseline.NonStdioFDCount, residue.End.NonStdioFDCount)
	}
	if residue.Baseline.DescendantCount != residue.End.DescendantCount {
		return fmt.Errorf("Agent descendant drift: baseline=%d end=%d", residue.Baseline.DescendantCount, residue.End.DescendantCount)
	}
	if residue.Baseline.TCPListenerCount != residue.End.TCPListenerCount || residue.Baseline.TCP6ListenerCount != residue.End.TCP6ListenerCount {
		return errors.New("Agent listener count drift")
	}
	return nil
}

func newLegacyFMRunID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate FM observation run id: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func verifyLegacyFMSentinels(sentinelPaths []string, sentinel []byte) error {
	for _, path := range sentinelPaths {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(content, sentinel) {
			return errors.New("outside-root sentinel changed")
		}
	}
	return nil
}
