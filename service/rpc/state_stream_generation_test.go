package rpc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/tsdb"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestReportSystemState_HandlerWaitsForMetricsBeforeReceipt(t *testing.T) {
	// Given
	reporter := requestTaskSecurityServer(9, 200, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	setupRequestTaskSecurityFixture(t, []*model.Server{reporter}, nil, map[uint64]model.UserInfo{200: {Role: model.RoleMember}}, map[string]uint64{"reporter-secret": 200})
	stop := make(chan struct{})
	stream := &stateGenerationHandlerStream{
		ctx:    metadata.NewIncomingContext(context.Background(), metadata.Pairs("client_secret", "reporter-secret", "client_uuid", reporter.UUID)),
		states: make(chan *pb.State, 1), receipts: make(chan *pb.Receipt, 1), stop: stop,
	}
	stream.states <- &pb.State{Uptime: 44}
	metricsStarted := make(chan *tsdb.ServerMetrics, 1)
	metricsRelease := make(chan struct{})
	oldWriter := writeServerMetrics
	writeServerMetrics = func(metrics *tsdb.ServerMetrics) error {
		metricsStarted <- metrics
		<-metricsRelease
		return nil
	}
	t.Cleanup(func() { writeServerMetrics = oldWriter })

	// When
	done := make(chan error, 1)
	go func() { done <- NewNezhaHandler().ReportSystemState(stream) }()
	metrics := <-metricsStarted
	select {
	case <-stream.receipts:
		t.Fatal("receipt sent before metrics writer completed")
	default:
	}
	close(metricsRelease)

	// Then
	require.Equal(t, reporter.ID, metrics.ServerID)
	require.Equal(t, uint64(44), metrics.Uptime)
	require.NotNil(t, <-stream.receipts)
	current, ok := singleton.ServerShared.Get(reporter.ID)
	require.True(t, ok)
	require.Equal(t, current.RuntimeSnapshot().LastActive, metrics.Timestamp)
	close(stop)
	require.ErrorIs(t, <-done, context.Canceled)
}

type stateGenerationStream struct{}

func (stateGenerationStream) Send(*pb.Receipt) error       { return nil }
func (stateGenerationStream) Recv() (*pb.State, error)     { return nil, nil }
func (stateGenerationStream) SetHeader(metadata.MD) error  { return nil }
func (stateGenerationStream) SendHeader(metadata.MD) error { return nil }
func (stateGenerationStream) SetTrailer(metadata.MD)       {}
func (stateGenerationStream) Context() context.Context     { return context.Background() }
func (stateGenerationStream) SendMsg(any) error            { return nil }
func (stateGenerationStream) RecvMsg(any) error            { return nil }

type stateGenerationHandlerStream struct {
	ctx      context.Context
	states   chan *pb.State
	receipts chan *pb.Receipt
	stop     <-chan struct{}
}

func (s *stateGenerationHandlerStream) Send(receipt *pb.Receipt) error {
	s.receipts <- receipt
	return nil
}

func (s *stateGenerationHandlerStream) Recv() (*pb.State, error) {
	select {
	case state := <-s.states:
		return state, nil
	case <-s.stop:
		return nil, context.Canceled
	}
}

func (s *stateGenerationHandlerStream) SetHeader(metadata.MD) error  { return nil }
func (s *stateGenerationHandlerStream) SendHeader(metadata.MD) error { return nil }
func (s *stateGenerationHandlerStream) SetTrailer(metadata.MD)       {}
func (s *stateGenerationHandlerStream) Context() context.Context     { return s.ctx }
func (s *stateGenerationHandlerStream) SendMsg(any) error            { return nil }
func (s *stateGenerationHandlerStream) RecvMsg(any) error            { return nil }

func TestReportSystemState_HandlerOldStreamCannotClearNewerState(t *testing.T) {
	// Given
	reporter := requestTaskSecurityServer(7, 200, "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
	setupRequestTaskSecurityFixture(t, []*model.Server{reporter}, nil, map[uint64]model.UserInfo{
		200: {Role: model.RoleMember},
	}, map[string]uint64{"reporter-secret": 200})
	oldStop := make(chan struct{})
	newStop := make(chan struct{})
	oldStream := &stateGenerationHandlerStream{
		ctx:    metadata.NewIncomingContext(context.Background(), metadata.Pairs("client_secret", "reporter-secret", "client_uuid", reporter.UUID)),
		states: make(chan *pb.State, 1), receipts: make(chan *pb.Receipt, 1), stop: oldStop,
	}
	newStream := &stateGenerationHandlerStream{
		ctx:    metadata.NewIncomingContext(context.Background(), metadata.Pairs("client_secret", "reporter-secret", "client_uuid", reporter.UUID)),
		states: make(chan *pb.State, 1), receipts: make(chan *pb.Receipt, 1), stop: newStop,
	}
	oldStream.states <- &pb.State{Uptime: 11}
	newStream.states <- &pb.State{Uptime: 22}
	handler := NewNezhaHandler()
	oldDone := make(chan error, 1)
	newDone := make(chan error, 1)
	go func() { oldDone <- handler.ReportSystemState(oldStream) }()
	<-oldStream.receipts

	// When
	go func() { newDone <- handler.ReportSystemState(newStream) }()
	<-newStream.receipts
	close(newStop)
	require.ErrorIs(t, <-newDone, context.Canceled)
	close(oldStop)
	require.ErrorIs(t, <-oldDone, context.Canceled)

	// Then
	server, ok := singleton.ServerShared.Get(reporter.ID)
	require.True(t, ok)
	require.Equal(t, uint64(22), server.State.Uptime)
	require.True(t, server.LastActive.IsZero())
}

func TestReportSystemState_OldStreamCannotUpdateNewerGeneration(t *testing.T) {
	// Given
	server := &model.Server{}
	model.InitServer(server)
	oldStream := stateGenerationStream{}
	newStream := stateGenerationStream{}
	oldLease := server.AttachStateStream(oldStream)
	updateGate := make(chan struct{})
	updateDone := make(chan bool, 1)
	oldState := &model.HostState{Uptime: 11}
	newState := &model.HostState{Uptime: 22}
	oldTime := time.Unix(100, 0)
	newTime := time.Unix(200, 0)
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		<-updateGate
		updateDone <- server.UpdateStateIfCurrent(oldLease, oldState, oldTime)
	}()

	// When
	newLease := server.AttachStateStream(newStream)
	close(updateGate)
	oldUpdateAccepted := <-updateDone
	newUpdateAccepted := server.UpdateStateIfCurrent(newLease, newState, newTime)
	waitGroup.Wait()

	// Then
	require.False(t, oldUpdateAccepted)
	require.True(t, newUpdateAccepted)
	require.Equal(t, newState, server.State)
	require.Equal(t, newTime, server.LastActive)
}

func TestReportSystemState_OldCleanupCannotClearNewerGeneration(t *testing.T) {
	// Given
	server := &model.Server{}
	model.InitServer(server)
	oldLease := server.AttachStateStream(stateGenerationStream{})
	newLease := server.AttachStateStream(stateGenerationStream{})
	state := &model.HostState{Uptime: 22}
	activeAt := time.Unix(200, 0)
	require.True(t, server.UpdateStateIfCurrent(newLease, state, activeAt))

	// When
	oldCleanup := server.ClearStateStreamIfCurrent(oldLease)

	// Then
	require.False(t, oldCleanup)
	require.Equal(t, state, server.State)
	require.Equal(t, activeAt, server.LastActive)
}

func TestReportSystemState_CurrentCleanupClearsOnlineVisibility(t *testing.T) {
	// Given
	server := &model.Server{}
	model.InitServer(server)
	lease := server.AttachStateStream(stateGenerationStream{})
	activeAt := time.Unix(300, 0)
	require.True(t, server.UpdateStateIfCurrent(lease, &model.HostState{Uptime: 33}, activeAt))

	// When
	cleared := server.ClearStateStreamIfCurrent(lease)

	// Then
	require.True(t, cleared)
	require.True(t, server.LastActive.IsZero())
}
