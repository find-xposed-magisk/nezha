//go:build linux

package scenario

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

type heldLegacyFMConnection interface {
	legacyFMFrameWriter
	Close() error
}

type heldLegacyFMPump interface {
	Events() <-chan client.Frame
	Done() <-chan struct{}
	Err() error
	Stop(context.Context) error
}

type heldLegacyFMCapabilityHandle interface {
	HeaderCapability() client.IOStreamCapability
	Wait(context.Context) (string, error)
	Cancel(context.Context) error
	Unregister(context.Context) error
	WaitExpectation(context.Context, *client.Client, client.IOStreamState, bool) error
}

type heldLegacyFMDependencies struct {
	SnapshotState func(context.Context, *client.Client) (client.IOStreamState, error)
	Register      func(context.Context, *client.Client, heldIOStreamCapabilityIdentity) (heldLegacyFMCapabilityHandle, error)
	CreateSession func(context.Context, *client.Client, uint64, client.IOStreamCapability) (string, error)
	DialWebSocket func(context.Context, *client.Client, string) (heldLegacyFMConnection, error)
	NewPump       func(context.Context, heldLegacyFMConnection, int) (heldLegacyFMPump, error)
	WaitForState  func(context.Context, *client.Client, client.IOStreamState, heldLegacyFMCapabilityHandle, bool) error
	RemoveFixture func(context.Context, string, string) error
}

func defaultHeldLegacyFMDependencies() heldLegacyFMDependencies {
	return heldLegacyFMDependencies{
		SnapshotState: func(ctx context.Context, transport *client.Client) (client.IOStreamState, error) {
			return transport.IOStreamState(ctx)
		},
		Register: func(ctx context.Context, transport *client.Client, identity heldIOStreamCapabilityIdentity) (heldLegacyFMCapabilityHandle, error) {
			return registerHeldIOStreamCapability(ctx, transport, identity)
		},
		CreateSession: func(ctx context.Context, transport *client.Client, serverID uint64, capability client.IOStreamCapability) (string, error) {
			return createLegacyFMSession(ctx, transport, serverID, capability)
		},
		DialWebSocket: func(ctx context.Context, transport *client.Client, path string) (heldLegacyFMConnection, error) {
			return transport.DialWebSocket(ctx, path)
		},
		NewPump: func(ctx context.Context, connection heldLegacyFMConnection, capacity int) (heldLegacyFMPump, error) {
			concrete, ok := connection.(*client.WebSocketConnection)
			if !ok {
				return nil, ErrInvalidHeldFramePump
			}
			return newHeldWebSocketPump(ctx, concrete, capacity)
		},
		WaitForState: func(ctx context.Context, transport *client.Client, baseline client.IOStreamState, capability heldLegacyFMCapabilityHandle, absent bool) error {
			return capability.WaitExpectation(ctx, transport, baseline, absent)
		},
		RemoveFixture: removeHeldLegacyFMFixture,
	}
}

func removeHeldLegacyFMFixture(ctx context.Context, workspaceRoot, fixtureRoot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanWorkspace := filepath.Clean(workspaceRoot)
	cleanFixture := filepath.Clean(fixtureRoot)
	relative, err := filepath.Rel(cleanWorkspace, cleanFixture)
	if err != nil || filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("FM fixture root escaped Agent workspace")
	}
	return os.RemoveAll(cleanFixture)
}
