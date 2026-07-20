//go:build agentcompat

package rpc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/stretchr/testify/require"
)

func TestMCPReceiptGate_FormatsTaskAndResultWithGeneration(t *testing.T) {
	// Given
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	gate := installReceiptGateForTest(serverConn)
	defer clearReceiptGateForTest()
	reader := bufio.NewReader(clientConn)

	// When
	taskDone := make(chan struct{})
	go func() {
		notifyMCPTaskDispatched(7, 9, model.TaskTypeExec)
		close(taskDone)
	}()
	taskLine := mustReadLine(t, reader)
	<-taskDone
	resultDone := make(chan struct{})
	go func() {
		notifyMCPTaskResultAccepted(7, 9, model.TaskTypeExec)
		close(resultDone)
	}()
	resultLine := mustReadLine(t, reader)
	<-resultDone

	// Then
	require.Equal(t, "task "+itoa(gate.generation)+" 7 9 "+itoa(model.TaskTypeExec)+"\n", taskLine)
	require.Equal(t, "result "+itoa(gate.generation)+" 7 9 "+itoa(model.TaskTypeExec)+"\n", resultLine)
}

func TestCallAgent_EmitsOneTaskAndOneAcceptedResult(t *testing.T) {
	// Given
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	gate := installReceiptGateForTest(serverConn)
	defer clearReceiptGateForTest()
	stream := newFakeStream()
	cleanup := installFakeServer(t, 801, stream)
	defer cleanup()
	reader := bufio.NewReader(clientConn)
	lines := make(chan string, 2)
	go func() {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		lines <- line
		line, err = reader.ReadString('\n')
		if err == nil {
			lines <- line
		}
	}()

	go func() {
		sent := <-stream.sent
		deliverMCPResult(&pb.TaskResult{Id: sent.GetId(), Type: sent.GetType(), Successful: true, Data: "{}"})
		deliverMCPResult(&pb.TaskResult{Id: sent.GetId(), Type: sent.GetType(), Successful: true, Data: "{}"})
	}()

	// When
	_, err := CallAgent(context.Background(), 801, model.TaskTypeExec, model.ExecRequest{Cmd: "x"}, time.Second)

	// Then
	require.NoError(t, err)
	taskLine := <-lines
	resultLine := <-lines
	require.Equal(t, "task "+itoa(gate.generation)+" 801 "+itoa(parseReceiptTaskID(taskLine))+" "+itoa(model.TaskTypeExec)+"\n", taskLine)
	require.Equal(t, "result "+itoa(gate.generation)+" 801 "+itoa(parseReceiptTaskID(resultLine))+" "+itoa(model.TaskTypeExec)+"\n", resultLine)
	require.Equal(t, parseReceiptTaskID(taskLine), parseReceiptTaskID(resultLine))
	clientConn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, readErr := reader.ReadString('\n')
	require.Error(t, readErr)
}

func TestCallAgent_SendFailureEmitsNoTaskReceipt(t *testing.T) {
	// Given
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	installReceiptGateForTest(serverConn)
	defer clearReceiptGateForTest()
	stream := &fakeTaskStream{sent: make(chan *pb.Task, 1), err: errors.New("send failed")}
	cleanup := installFakeServer(t, 802, stream)
	defer cleanup()

	// When
	_, err := CallAgent(context.Background(), 802, model.TaskTypeExec, model.ExecRequest{Cmd: "x"}, time.Second)

	// Then
	require.EqualError(t, err, "send failed")
	clientConn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, readErr := bufio.NewReader(clientConn).ReadString('\n')
	require.Error(t, readErr)
}

func TestCallAgent_LateDuplicateAndCancelledResultsEmitNoAcceptedReceipt(t *testing.T) {
	// Given
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	installReceiptGateForTest(serverConn)
	defer clearReceiptGateForTest()
	stream := newFakeStream()
	cleanup := installFakeServer(t, 803, stream)
	defer cleanup()
	reader := bufio.NewReader(clientConn)
	taskLineCh := make(chan string, 1)

	// When
	taskIDCh := make(chan uint64, 1)
	go func() {
		sent := <-stream.sent
		taskIDCh <- sent.GetId()
		line, _ := reader.ReadString('\n')
		taskLineCh <- line
	}()
	_, err := CallAgent(context.Background(), 803, model.TaskTypeFsRead, model.FsReadRequest{Path: "/x"}, 20*time.Millisecond)
	require.ErrorIs(t, err, ErrAgentTimeout)
	taskID := <-taskIDCh
	deliverMCPResult(&pb.TaskResult{Id: taskID, Type: model.TaskTypeFsRead, Successful: true, Data: "{}"})
	deliverMCPResult(&pb.TaskResult{Id: taskID, Type: model.TaskTypeFsRead, Successful: true, Data: "{}"})

	// Then
	require.Contains(t, <-taskLineCh, "task ")
	clientConn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, readErr := reader.ReadString('\n')
	require.Error(t, readErr)
}

func TestCallAgent_CancelledResultEmitsNoAcceptedReceipt(t *testing.T) {
	// Given
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	installReceiptGateForTest(serverConn)
	defer clearReceiptGateForTest()
	stream := newFakeStream()
	cleanup := installFakeServer(t, 804, stream)
	defer cleanup()
	reader := bufio.NewReader(clientConn)
	taskLineCh := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		taskLineCh <- line
	}()
	taskIDCh := make(chan uint64, 1)
	go func() {
		sent := <-stream.sent
		taskIDCh <- sent.GetId()
	}()

	// When
	errCh := make(chan error, 1)
	go func() {
		_, err := CallAgent(context.Background(), 804, model.TaskTypeExec, model.ExecRequest{Cmd: "x"}, time.Second)
		errCh <- err
	}()
	taskID := <-taskIDCh
	CancelAllMCPInflight()
	deliverMCPResult(&pb.TaskResult{Id: taskID, Type: model.TaskTypeExec, Successful: true, Data: "{}"})

	// Then
	require.ErrorIs(t, <-errCh, ErrMCPDisabled)
	require.Contains(t, <-taskLineCh, "task ")
	clientConn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, readErr := reader.ReadString('\n')
	require.Error(t, readErr)
}

func itoa(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func parseReceiptTaskID(line string) uint64 {
	fields := strings.Fields(line)
	value, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil {
		panic(fmt.Sprintf("invalid receipt line %q: %v", line, err))
	}
	return value
}
