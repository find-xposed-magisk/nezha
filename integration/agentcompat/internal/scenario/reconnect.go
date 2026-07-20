//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

type ReconnectInput struct {
	Paths          contract.Paths
	DashboardFault string
}

type ReconnectObservation struct {
	ServerID       uint64
	UUID           string
	OldGeneration  uint64
	NewGeneration  uint64
	DisconnectAt   time.Time
	ReconnectAt    time.Time
	TaskIDs        []uint64
	ResultIDs      []uint64
	PostReconnect  bool
	AgentRestarted bool
}

type ReconnectResult struct {
	Observation ReconnectObservation `json:"observation"`
	CleanupOK   bool                 `json:"cleanup_ok"`
	Passed      bool                 `json:"passed"`
	Error       string               `json:"error,omitempty"`
}

type Reconnect struct{}

func (scenario Reconnect) Run(ctx context.Context, input ReconnectInput) (Result, error) {
	result, _, err := scenario.RunWithEvidence(ctx, input)
	return result, err
}

func (observation ReconnectObservation) Validate() error {
	if observation.ServerID == 0 || observation.UUID == "" {
		return errors.New("reconnect observation omitted server identity")
	}
	if observation.OldGeneration == 0 || observation.NewGeneration <= observation.OldGeneration {
		return errors.New("reconnect generations are not strictly increasing")
	}
	if observation.DisconnectAt.IsZero() || observation.ReconnectAt.IsZero() || !observation.ReconnectAt.After(observation.DisconnectAt) {
		return errors.New("reconnect timestamps are not ordered")
	}
	if len(observation.TaskIDs) == 0 || !slices.Equal(observation.TaskIDs, observation.ResultIDs) {
		return errors.New("reconnect task and result IDs differ")
	}
	seenTaskIDs := make(map[uint64]struct{}, len(observation.TaskIDs))
	for _, taskID := range observation.TaskIDs {
		if _, exists := seenTaskIDs[taskID]; exists {
			return fmt.Errorf("reconnect task ID %d was duplicated", taskID)
		}
		seenTaskIDs[taskID] = struct{}{}
	}
	if !observation.PostReconnect || !observation.AgentRestarted {
		return errors.New("reconnect post-process checks were not completed")
	}
	return nil
}

func (observation ReconnectObservation) ReconnectInterval() time.Duration {
	return observation.ReconnectAt.Sub(observation.DisconnectAt)
}
