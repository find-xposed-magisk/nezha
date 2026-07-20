package rpc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/tsdb"
)

func TestStateMetricsWriterRunsOnlyForCurrentGeneration(t *testing.T) {
	// Given
	server := &model.Server{}
	model.InitServer(server)
	oldLease := server.AttachStateStream(stateGenerationStream{})
	newLease := server.AttachStateStream(stateGenerationStream{})
	oldCalls := 0
	newCalls := 0
	oldWriter := writeServerMetrics
	writeServerMetrics = func(*tsdb.ServerMetrics) error {
		newCalls++
		return nil
	}
	t.Cleanup(func() { writeServerMetrics = oldWriter })

	// When
	oldAccepted := server.UpdateStateIfCurrentWithSideEffect(oldLease, &model.HostState{Uptime: 11}, time.Unix(100, 0), func() error {
		oldCalls++
		return writeServerMetrics(&tsdb.ServerMetrics{ServerID: 7, Timestamp: time.Unix(100, 0)})
	})
	newAccepted := server.UpdateStateIfCurrentWithSideEffect(newLease, &model.HostState{Uptime: 22}, time.Unix(200, 0), func() error {
		return writeServerMetrics(&tsdb.ServerMetrics{ServerID: 7, Timestamp: time.Unix(200, 0)})
	})

	// Then
	require.False(t, oldAccepted)
	require.True(t, newAccepted)
	require.Zero(t, oldCalls)
	require.Equal(t, 1, newCalls)
}
