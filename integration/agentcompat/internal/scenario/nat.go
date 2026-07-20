//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

type NATInput struct {
	Paths contract.Paths
	Fault contract.Fault
}

type NAT struct{}

type natForm struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	ServerID uint64 `json:"server_id"`
	Host     string `json:"host"`
	Domain   string `json:"domain"`
}

type natServerListRequest struct {
	OnlineOnly bool `json:"online_only"`
}

type natServerListResponse struct {
	Servers []struct {
		ID     uint64 `json:"id"`
		UUID   string `json:"uuid"`
		Online bool   `json:"online"`
	} `json:"servers"`
}

type natIDResponse uint64

const natTestDomain = "agentcompat-nat.invalid"
const natHalfCloseTestDomain = "half-close.agentcompat-nat.invalid"

func (NAT) Run(ctx context.Context, input NATInput) (result Result, runErr error) {
	assertions := NewAssertionSet()
	dashboardInstance, err := dashboard.Start(ctx, dashboard.StartConfig{SourceDir: input.Paths.NezhaSource().String(), ReceiptGate: true})
	if err != nil {
		return Result{Name: "nat", Assertions: assertions.Results(), Error: errorText(err)}, err
	}
	var ordinaryBackend *fixture.NATEchoBackend
	var halfCloseBackend *fixture.NATEchoBackend
	var agentInstance *agent.Agent
	defer func() {
		cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		var cleanupErr error
		if agentInstance != nil {
			cleanupErr = errors.Join(cleanupErr, agentInstance.Stop(cleanupContext))
		}
		if ordinaryBackend != nil {
			cleanupErr = errors.Join(cleanupErr, ordinaryBackend.Close())
		}
		if halfCloseBackend != nil {
			cleanupErr = errors.Join(cleanupErr, halfCloseBackend.Close())
		}
		cleanupErr = errors.Join(cleanupErr, dashboardInstance.Stop(cleanupContext))
		agentCleanupPassed := agentInstance == nil || agentInstance.CleanupReceipt().Passed
		result.CleanupOK = cleanupErr == nil && agentCleanupPassed && dashboardInstance.CleanupReceipt().Passed
		if cleanupErr != nil && runErr == nil {
			runErr = cleanupErr
			result.Passed = false
			result.Error = errorText(cleanupErr)
		}
	}()
	ordinaryBackend, err = fixture.StartNATEchoBackend()
	if err != nil {
		return finishNAT(assertions, err)
	}
	halfCloseBackend, err = fixture.StartNATResponseHalfCloseEchoBackend()
	if err != nil {
		return finishNAT(assertions, err)
	}
	agentInstance, err = agent.Start(ctx, agent.AgentStartConfig{SourceDir: input.Paths.AgentSource().String(), Endpoint: dashboardInstance.Endpoint(), Secret: dashboardInstance.AgentSecret(), UUID: "00000000-0000-0000-0000-000000000114"})
	if err != nil {
		return finishNAT(assertions, err)
	}
	if err := dashboardInstance.WaitForReceiptAccepted(ctx); err != nil {
		return finishNAT(assertions, err)
	}
	if err := dashboardInstance.ReleaseReceipt(ctx); err != nil {
		return finishNAT(assertions, err)
	}
	if _, err := agentInstance.WaitReady(ctx, dashboardInstance); err != nil {
		return finishNAT(assertions, err)
	}
	serverID, err := natPrimaryServerID(ctx, dashboardInstance, agentInstance.UUID())
	if err != nil {
		return finishNAT(assertions, err)
	}
	admin := dashboardInstance.Clients().REST
	created, err := client.DoREST[natForm, natIDResponse](ctx, admin, client.RESTRequest[natForm]{Method: http.MethodPost, Path: "/api/v1/nat", Body: &natForm{Name: "agentcompat-nat", Enabled: true, ServerID: serverID, Host: ordinaryBackend.Address(), Domain: natTestDomain}})
	if err != nil {
		return finishNAT(assertions, err)
	}
	profileID := uint64(created)
	ordinaryRequest := natHTTPRequestSpec{Endpoint: dashboardInstance.Endpoint(), Host: natTestDomain, Method: "PATCH", Path: "/nat?case=ordinary", Body: "ordinary"}
	response, responseRecord, err := natHTTPRoundTrip(ctx, ordinaryRequest)
	connectionErr := ordinaryBackend.WaitConnection(ctx)
	if err == nil {
		err = connectionErr
	}
	record, recordErr := ordinaryBackend.WaitRequest(ctx)
	if err == nil {
		err = recordErr
	}
	assertions.Record("enabled profile traverses exact HTTP request and response", err == nil && response.Status == http.StatusOK && response.PeerWriteClosed && response.LegacyCloseMarker == io.EOF.Error() && response.Body == natExpectedBody(ordinaryRequest) && natExactRequestObserved(ordinaryRequest, responseRecord, record), errorText(err))
	if err != nil {
		return finishNAT(assertions, err)
	}
	halfCloseProfile, err := client.DoREST[natForm, natIDResponse](ctx, admin, client.RESTRequest[natForm]{Method: http.MethodPost, Path: "/api/v1/nat", Body: &natForm{Name: "agentcompat-nat-half-close", Enabled: true, ServerID: serverID, Host: halfCloseBackend.Address(), Domain: natHalfCloseTestDomain}})
	if err != nil {
		return finishNAT(assertions, err)
	}
	// The fixture half-closes its response after writing it. Requiring a client
	// request half-close would deadlock HTTP handling because Dashboard keeps the
	// request side open while waiting for the backend response.
	halfCloseRequest := natHTTPRequestSpec{Endpoint: dashboardInstance.Endpoint(), Host: natHalfCloseTestDomain, Method: "POST", Path: "/nat?case=half-close", Body: "half-closed"}
	halfCloseResponse, halfCloseResponseRecord, err := natHTTPRoundTrip(ctx, halfCloseRequest)
	halfCloseConnectionErr := halfCloseBackend.WaitConnection(ctx)
	if err == nil {
		err = halfCloseConnectionErr
	}
	halfCloseRecord, halfCloseRecordErr := halfCloseBackend.WaitRequest(ctx)
	if err == nil {
		err = halfCloseRecordErr
	}
	assertions.Record("backend half-close traverses response and exact request", err == nil && halfCloseResponse.Status == http.StatusOK && halfCloseResponse.PeerWriteClosed && halfCloseResponse.LegacyCloseMarker == io.EOF.Error() && halfCloseResponse.Body == natExpectedBody(halfCloseRequest) && natExactRequestObserved(halfCloseRequest, halfCloseResponseRecord, halfCloseRecord) && halfCloseRecord.ResponseHalfClosed, errorText(err))
	if err != nil {
		return finishNAT(assertions, err)
	}
	if _, err := client.DoREST[[]uint64, struct{}](ctx, admin, client.RESTRequest[[]uint64]{Method: http.MethodPost, Path: "/api/v1/batch-delete/nat", Body: &[]uint64{uint64(halfCloseProfile)}}); err != nil {
		return finishNAT(assertions, err)
	}
	limited, err := createScopedClient(ctx, dashboardInstance, []string{"nezha:server:read"})
	if err != nil {
		return finishNAT(assertions, err)
	}
	_, unauthorizedErr := client.DoREST[natForm, natIDResponse](ctx, limited, client.RESTRequest[natForm]{Method: http.MethodPost, Path: "/api/v1/nat", Body: &natForm{Name: "unauthorized", Enabled: true, ServerID: serverID, Host: ordinaryBackend.Address(), Domain: "unauthorized." + natTestDomain}})
	assertions.Record("unauthorized NAT profile is rejected", isForbidden(unauthorizedErr), errorText(unauthorizedErr))
	disabled, err := client.DoREST[natForm, natIDResponse](ctx, admin, client.RESTRequest[natForm]{Method: http.MethodPost, Path: "/api/v1/nat", Body: &natForm{Name: "disabled", Enabled: false, ServerID: serverID, Host: ordinaryBackend.Address(), Domain: "disabled." + natTestDomain}})
	if err != nil {
		return finishNAT(assertions, err)
	}
	disabledResponse, err := natHTTPStatus(ctx, dashboardInstance.Endpoint(), "disabled."+natTestDomain)
	disabledRouteErr := natAssertNoBackendConnection(ctx, ordinaryBackend)
	assertions.Record("disabled NAT profile is blocked", err == nil && disabledRouteErr == nil && disabledResponse.Status == http.StatusForbidden, errorText(errors.Join(err, disabledRouteErr)))
	if _, err := client.DoREST[[]uint64, struct{}](ctx, admin, client.RESTRequest[[]uint64]{Method: http.MethodPost, Path: "/api/v1/batch-delete/nat", Body: &[]uint64{uint64(disabled)}}); err != nil {
		return finishNAT(assertions, err)
	}
	if _, err := client.DoREST[[]uint64, struct{}](ctx, admin, client.RESTRequest[[]uint64]{Method: http.MethodPost, Path: "/api/v1/batch-delete/nat", Body: &[]uint64{profileID}}); err != nil {
		return finishNAT(assertions, err)
	}
	deletedResponse, err := natHTTPStatus(ctx, dashboardInstance.Endpoint(), natTestDomain)
	deletedRouteErr := natAssertNoBackendConnection(ctx, ordinaryBackend)
	assertions.Record("deleted NAT profile no longer routes", err == nil && natDeletedRouteObserved(deletedResponse, deletedRouteErr, natHTTPRequestSpec{Host: natTestDomain, Method: http.MethodGet, Path: "/"}), errorText(errors.Join(err, deletedRouteErr)))
	return finishNAT(assertions, nil)
}

func natExactRequestObserved(request natHTTPRequestSpec, responseRecord, backendRecord fixture.NATEchoRecord) bool {
	for _, record := range []fixture.NATEchoRecord{responseRecord, backendRecord} {
		if record.Method != request.Method || record.Path != request.Path || record.Host != request.Host || record.HeaderValue != natEchoHeaderValue || string(record.Body) != request.Body {
			return false
		}
	}
	return true
}

func natDeletedRouteObserved(response natRawResponse, routeErr error, request natHTTPRequestSpec) bool {
	return routeErr == nil && response.Status == http.StatusOK && response.Body != "" && response.Body != natExpectedBody(request)
}

func finishNAT(assertions *AssertionSet, runErr error) (Result, error) {
	for _, assertion := range assertions.Results() {
		if !assertion.Passed && runErr == nil {
			runErr = fmt.Errorf("%s: %s", assertion.Name, assertion.Details)
		}
	}
	result := Result{Name: "nat", Passed: runErr == nil, Assertions: assertions.Results(), Error: errorText(runErr), CleanupOK: false}
	return result, runErr
}

func natPrimaryServerID(ctx context.Context, dashboardInstance *dashboard.Dashboard, uuid string) (uint64, error) {
	response, err := client.CallTool[natServerListRequest, natServerListResponse](ctx, dashboardInstance.Clients().MCP, client.ToolCall[natServerListRequest]{Name: "server.list", Arguments: natServerListRequest{OnlineOnly: true}})
	if err != nil {
		return 0, err
	}
	for _, server := range response.StructuredContent.Servers {
		if server.UUID == uuid && server.Online {
			return server.ID, nil
		}
	}
	return 0, errors.New("primary agent is not online")
}
