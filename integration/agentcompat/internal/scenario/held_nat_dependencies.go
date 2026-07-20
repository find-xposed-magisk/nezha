//go:build linux

package scenario

import (
	"context"
	"net/http"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

type heldNATDependencies struct {
	snapshotState        func(context.Context, *client.Client) (client.IOStreamState, error)
	createProfile        func(context.Context, *dashboard.Dashboard, *fixture.NATHoldBackend, uint64, string, string) (uint64, error)
	deleteProfile        func(context.Context, *dashboard.Dashboard, uint64) error
	register             func(context.Context, *client.Client, heldIOStreamCapabilityIdentity) (*heldIOStreamCapability, error)
	startRequest         func(context.Context, string, string, string, client.IOStreamCapability) (*heldNATRequest, error)
	waitRequestObserved  func(context.Context, *fixture.NATHoldBackend) error
	waitRequest          func(context.Context, *fixture.NATHoldBackend) (fixture.NATEchoRecord, error)
	proveRequest         func(fixture.NATEchoRecord, string, string) error
	waitCapability       func(context.Context, *heldIOStreamCapability) (string, error)
	setStreamID          func(*heldSessionLifecycle, string) error
	waitExpectation      func(context.Context, *heldIOStreamCapability, *client.Client, client.IOStreamState, bool) error
	closeBackend         func(*fixture.NATHoldBackend) error
	closeRequest         func(*heldNATRequest) error
	cancelCapability     func(context.Context, *heldIOStreamCapability) error
	unregisterCapability func(context.Context, *heldIOStreamCapability) error
}

func defaultHeldNATDependencies() heldNATDependencies {
	return heldNATDependencies{
		snapshotState: func(ctx context.Context, transport *client.Client) (client.IOStreamState, error) {
			return transport.IOStreamState(ctx)
		},
		createProfile: func(ctx context.Context, dashboardInstance *dashboard.Dashboard, backend *fixture.NATHoldBackend, serverID uint64, name, domain string) (uint64, error) {
			created, err := client.DoREST[natForm, natIDResponse](ctx, dashboardInstance.Clients().REST, client.RESTRequest[natForm]{Method: http.MethodPost, Path: "/api/v1/nat", Body: &natForm{Name: name, Enabled: true, ServerID: serverID, Host: backend.Address(), Domain: domain}})
			return uint64(created), err
		},
		deleteProfile: func(ctx context.Context, dashboardInstance *dashboard.Dashboard, profileID uint64) error {
			_, err := client.DoREST[[]uint64, struct{}](ctx, dashboardInstance.Clients().REST, client.RESTRequest[[]uint64]{Method: http.MethodPost, Path: "/api/v1/batch-delete/nat", Body: &[]uint64{profileID}})
			return err
		},
		register: func(ctx context.Context, transport *client.Client, identity heldIOStreamCapabilityIdentity) (*heldIOStreamCapability, error) {
			return registerHeldIOStreamCapability(ctx, transport, identity)
		},
		startRequest: startHeldNATRequest,
		waitRequestObserved: func(ctx context.Context, backend *fixture.NATHoldBackend) error {
			select {
			case <-backend.RequestObserved():
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		waitRequest: func(ctx context.Context, backend *fixture.NATHoldBackend) (fixture.NATEchoRecord, error) {
			return backend.WaitRequest(ctx)
		},
		proveRequest: proveHeldNATRequest,
		waitCapability: func(ctx context.Context, capability *heldIOStreamCapability) (string, error) {
			return capability.Wait(ctx)
		},
		setStreamID: func(lifecycle *heldSessionLifecycle, streamID string) error {
			return lifecycle.setIOStreamID(streamID)
		},
		waitExpectation: func(ctx context.Context, capability *heldIOStreamCapability, stateClient *client.Client, baseline client.IOStreamState, absent bool) error {
			return capability.waitExpectation(ctx, stateClient, baseline, absent)
		},
		closeBackend:         func(backend *fixture.NATHoldBackend) error { return backend.Close() },
		closeRequest:         func(request *heldNATRequest) error { return request.close() },
		cancelCapability:     func(ctx context.Context, capability *heldIOStreamCapability) error { return capability.Cancel(ctx) },
		unregisterCapability: func(ctx context.Context, capability *heldIOStreamCapability) error { return capability.Unregister(ctx) },
	}
}

func activeHeldNATDependencies() heldNATDependencies {
	return defaultHeldNATDependencies()
}
