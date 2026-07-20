//go:build agentcompat

package rpc

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func installReceiptGateForTest(conn net.Conn) *receiptGate {
	activeReceiptGateMu.Lock()
	receiptGateGeneration++
	generation := receiptGateGeneration
	activeReceiptGateMu.Unlock()
	gate := newReceiptGate(conn, generation)
	activeReceiptGateMu.Lock()
	activeReceiptGate = gate
	activeReceiptGateMu.Unlock()
	return gate
}

func clearReceiptGateForTest() {
	activeReceiptGateMu.Lock()
	gate := activeReceiptGate
	activeReceiptGate = nil
	activeReceiptGateMu.Unlock()
	if gate != nil {
		gate.close()
	}
}

func TestReceiptGate_EOFResetsGate(t *testing.T) {
	// Given
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	installReceiptGateForTest(serverConn)
	defer clearReceiptGateForTest()
	gate := currentReceiptGate()
	require.NotNil(t, gate)
	go func() {
		reader := bufio.NewReader(clientConn)
		_, _ = reader.ReadString('\n')
		_ = clientConn.Close()
	}()

	// When
	err := notifyReceiptAccepted(7, "uuid", 1, 1)

	// Then
	require.Error(t, err)
	activeReceiptGateMu.RLock()
	active := activeReceiptGate
	activeReceiptGateMu.RUnlock()
	require.Nil(t, active)
}

func TestReceiptGate_ListenerAcceptsAndReplacesConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer CloseReceiptGate()
	SetReceiptGateListener(listener)

	oldClient, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	oldReader := bufio.NewReader(oldClient)
	require.Equal(t, "ready\n", mustReadLine(t, oldReader))

	newClient, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	defer newClient.Close()
	newReader := bufio.NewReader(newClient)
	require.Equal(t, "ready\n", mustReadLine(t, newReader))
	_ = oldClient.SetReadDeadline(time.Now().Add(time.Second))
	_, oldErr := oldReader.ReadString('\n')
	require.Error(t, oldErr)
}

func TestReceiptGate_CloseInterruptsHeldReadAndQueuedWrite(t *testing.T) {
	// Given
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	installReceiptGateForTest(serverConn)
	acceptedStarted := make(chan struct{})
	acceptedDone := make(chan error, 1)
	go func() {
		close(acceptedStarted)
		acceptedDone <- notifyReceiptAccepted(7, "uuid", 1, 1)
	}()
	<-acceptedStarted
	reader := bufio.NewReader(clientConn)
	require.Equal(t, "accepted 7 uuid "+fmt.Sprint(currentReceiptGate().generation)+" 1 1\n", mustReadLine(t, reader))

	infoStarted := make(chan struct{})
	infoDone := make(chan error, 1)
	go func() {
		close(infoStarted)
		infoDone <- notifyInfo2(9, "held")
	}()
	<-infoStarted

	// When
	CloseReceiptGate()

	// Then
	select {
	case <-acceptedDone:
	case <-time.After(time.Second):
		t.Fatal("held receipt read was not interrupted")
	}
	select {
	case <-infoDone:
	case <-time.After(time.Second):
		t.Fatal("queued notification write was not released")
	}
}

func mustReadLine(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	return line
}

func TestReceiptGate_MalformedCommandResetsGate(t *testing.T) {
	// Given
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	installReceiptGateForTest(serverConn)
	defer clearReceiptGateForTest()
	go func() {
		reader := bufio.NewReader(clientConn)
		_, _ = reader.ReadString('\n')
		_, _ = clientConn.Write([]byte("hold\n"))
	}()

	// When
	err := notifyReceiptAccepted(7, "uuid", 1, 1)

	// Then
	require.EqualError(t, err, "receipt gate received unexpected command")
	activeReceiptGateMu.RLock()
	active := activeReceiptGate
	activeReceiptGateMu.RUnlock()
	require.Nil(t, active)
}

func TestReceiptGate_ReplacementClosesOldConnection(t *testing.T) {
	t.Run("replacement closes old connection", func(t *testing.T) {
		// Given
		oldServer, oldClient := net.Pipe()
		newServer, newClient := net.Pipe()
		t.Cleanup(func() { require.NoError(t, oldClient.Close()) })
		t.Cleanup(func() { require.NoError(t, newClient.Close()) })
		oldGate := installReceiptGateForTest(oldServer)
		t.Cleanup(oldGate.close)
		t.Cleanup(clearReceiptGateForTest)
		newGate := newReceiptGate(newServer, oldGate.generation+1)
		activeReceiptGateMu.Lock()
		activeReceiptGate = newGate
		activeReceiptGateMu.Unlock()
		oldDone := make(chan error, 1)
		go func() { oldDone <- oldGate.sendAccepted(7, "uuid", 1, 1) }()
		reader := bufio.NewReader(oldClient)
		_, _ = reader.ReadString('\n')

		// When
		oldGate.close()

		// Then
		select {
		case err := <-oldDone:
			require.Error(t, err)
		case <-time.After(time.Second):
			t.Fatal("old receipt gate remained blocked after replacement")
		}
	})

	require.Nil(t, currentReceiptGate())
}

func TestReceiptGate_Info2AndReceiptNotificationsSerialize(t *testing.T) {
	// Given
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	installReceiptGateForTest(serverConn)
	defer clearReceiptGateForTest()
	gate := currentReceiptGate()
	require.NotNil(t, gate)
	lines := make(chan string, 2)
	go func() {
		reader := bufio.NewReader(clientConn)
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		lines <- strings.TrimSpace(line)
		_, _ = clientConn.Write([]byte("release\n"))
		line, err = reader.ReadString('\n')
		if err == nil {
			lines <- strings.TrimSpace(line)
		}
	}()

	// When
	acceptedDone := make(chan error, 1)
	go func() { acceptedDone <- notifyReceiptAccepted(7, "uuid", 1, 1) }()
	select {
	case err := <-acceptedDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.NoError(t, <-acceptedDone)
	}
	require.NoError(t, notifyInfo2(7, "uuid"))

	// Then
	require.Equal(t, "accepted 7 uuid "+fmt.Sprint(gate.generation)+" 1 1", <-lines)
	require.Equal(t, "info2 "+fmt.Sprint(gate.generation)+" 7 uuid", <-lines)
}
