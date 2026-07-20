//go:build linux && agentcompat

package scenario

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReconnectObservation_RejectsNonIncreasingGenerations(t *testing.T) {
	// Given
	observation := ReconnectObservation{OldGeneration: 4, NewGeneration: 4}

	// When
	err := observation.Validate()

	// Then
	require.Error(t, err)
}

func TestReconnectObservation_RequiresUniqueCompleteTaskIDs(t *testing.T) {
	// Given
	observation := ReconnectObservation{
		OldGeneration: 2,
		NewGeneration: 3,
		DisconnectAt:  time.Unix(10, 0),
		ReconnectAt:   time.Unix(11, 0),
		TaskIDs:       []uint64{7, 7},
		ResultIDs:     []uint64{7},
	}

	// When
	err := observation.Validate()

	// Then
	require.Error(t, err)
}

func TestReconnectObservation_RejectsNonAdjacentDuplicateTaskIDs(t *testing.T) {
	// Given
	observation := ReconnectObservation{
		ServerID:       7,
		UUID:           "00000000-0000-0000-0000-000000000111",
		OldGeneration:  2,
		NewGeneration:  3,
		DisconnectAt:   time.Unix(10, 0),
		ReconnectAt:    time.Unix(11, 0),
		TaskIDs:        []uint64{7, 8, 7},
		ResultIDs:      []uint64{7, 8, 7},
		PostReconnect:  true,
		AgentRestarted: true,
	}

	// When
	err := observation.Validate()

	// Then
	require.Error(t, err)
}

func TestReconnectObservation_RecordsReconnectInterval(t *testing.T) {
	// Given
	observation := ReconnectObservation{
		ServerID:       7,
		UUID:           "00000000-0000-0000-0000-000000000111",
		OldGeneration:  2,
		NewGeneration:  3,
		DisconnectAt:   time.Unix(10, 0),
		ReconnectAt:    time.Unix(11, 0),
		TaskIDs:        []uint64{7},
		ResultIDs:      []uint64{7},
		PostReconnect:  true,
		AgentRestarted: true,
	}

	// When
	err := observation.Validate()

	// Then
	require.NoError(t, err)
	require.Equal(t, time.Second, observation.ReconnectInterval())
}
