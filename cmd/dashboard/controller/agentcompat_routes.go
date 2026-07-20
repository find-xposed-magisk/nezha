//go:build agentcompat

package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/rpc"
)

const (
	agentcompatOversizeWriteSentinel    = "agentcompat:oversize-write-contract"
	agentcompatOversizeWriteServerPath  = "/tmp/agentcompat-oversize-contract.txt"
	agentcompatFsWriteOperationOversize = "oversize"
)

type agentcompatFsWriteContractRequest struct {
	ServerID  uint64                      `json:"server_id"`
	Operation agentcompatFsWriteOperation `json:"operation"`
}

type agentcompatFsWriteOperation string

type agentcompatFsWriteContractResponse struct {
	Result           model.FsWriteResult `json:"result"`
	AgentRPCResponse bool                `json:"agent_rpc_response"`
}

func registerAgentcompatRoutes(router *gin.Engine) {
	patAuth := requiredAgentcompatPAT(apiTokenAuthMiddleware())
	router.POST("/agentcompat/fs-write-contract", patAuth, commonHandler(agentcompatFsWriteContract))
	router.POST("/agentcompat/mcp-rate-limit-probe", patAuth, commonHandler(agentcompatMCPRateLimitProbeRoute))
	router.GET("/agentcompat/io-stream-state", patAuth, commonHandler(agentcompatIOStreamSnapshot))
	router.POST("/agentcompat/io-stream-state", patAuth, commonHandler(agentcompatIOStreamWait))
	router.POST("/agentcompat/io-stream-quota-probe", patAuth, commonHandler(agentcompatIOStreamQuotaProbeRoute))
	registerAgentcompatCapabilityRoutes(router, patAuth)
	registerAgentcompatSQLiteHoldRoutes(router, patAuth)
}

type agentcompatIOStreamQuotaProbeResponse struct {
	UserAccepted   int  `json:"user_accepted"`
	UserRejected   int  `json:"user_rejected"`
	ServerAccepted int  `json:"server_accepted"`
	ServerRejected int  `json:"server_rejected"`
	Clean          bool `json:"clean"`
}

func agentcompatIOStreamQuotaProbeRoute(context *gin.Context) (agentcompatIOStreamQuotaProbeResponse, error) {
	if rpc.NezhaHandlerSingleton == nil {
		return agentcompatIOStreamQuotaProbeResponse{}, errors.New("IOStream handler is unavailable")
	}
	result := rpc.RunIOStreamQuotaProbe(context.Request.Context())
	if result.Err != nil {
		return agentcompatIOStreamQuotaProbeResponse{}, result.Err
	}
	return agentcompatIOStreamQuotaProbeResponse{UserAccepted: result.UserAccepted, UserRejected: result.UserRejected, ServerAccepted: result.ServerAccepted, ServerRejected: result.ServerRejected, Clean: result.TrackedStreams == 0}, nil
}

func requiredAgentcompatPAT(auth gin.HandlerFunc) gin.HandlerFunc {
	return func(context *gin.Context) {
		auth(context)
		if context.IsAborted() {
			return
		}
		if APITokenFromContext(context) == nil {
			abortAPITokenUnauthorized(context, "api token required")
		}
	}
}

func agentcompatIOStreamSnapshot(*gin.Context) (rpc.IOStreamState, error) {
	if rpc.NezhaHandlerSingleton == nil {
		return rpc.IOStreamState{}, errors.New("IOStream handler is unavailable")
	}
	return rpc.NezhaHandlerSingleton.SnapshotIOStreamState(), nil
}

func agentcompatIOStreamWait(context *gin.Context) (rpc.IOStreamState, error) {
	if rpc.NezhaHandlerSingleton == nil {
		return rpc.IOStreamState{}, errors.New("IOStream handler is unavailable")
	}
	var expectation rpc.IOStreamStateExpectation
	if err := context.ShouldBindJSON(&expectation); err != nil {
		return rpc.IOStreamState{}, err
	}
	return rpc.NezhaHandlerSingleton.WaitForIOStreamState(context.Request.Context(), expectation)
}

func agentcompatFsWriteContract(context *gin.Context) (agentcompatFsWriteContractResponse, error) {
	var request agentcompatFsWriteContractRequest
	if err := decodeAgentcompatJSON(context, &request); err != nil {
		return agentcompatFsWriteContractResponse{}, err
	}
	if request.ServerID == 0 || request.Operation == "" {
		return agentcompatFsWriteContractResponse{}, errors.New("server_id and operation are required")
	}
	if request.Operation != agentcompatFsWriteOperation(agentcompatFsWriteOperationOversize) {
		return agentcompatFsWriteContractResponse{}, errors.New("unknown filesystem write operation")
	}
	token := APITokenFromContext(context)
	if token == nil || !token.HasScope(model.ScopeServerWrite) {
		return agentcompatFsWriteContractResponse{}, errors.New("missing required scope: " + model.ScopeServerWrite)
	}
	server, err := requireServerAccess(context, request.ServerID)
	if err != nil {
		return agentcompatFsWriteContractResponse{}, err
	}
	content := "denied"
	if request.Operation == agentcompatFsWriteOperation(agentcompatFsWriteOperationOversize) {
		content = agentcompatOversizeWriteSentinel
	}
	// This tagged probe owns both path and payload; accepting either from callers would make it an arbitrary-write endpoint.
	raw, err := rpc.CallAgent(context.Request.Context(), server.ID, model.TaskTypeFsWrite, model.FsWriteRequest{
		Path: agentcompatOversizeWriteServerPath, Content: content, Encoding: "utf8", Mode: "0600",
	}, 30*time.Second)
	if err != nil {
		return agentcompatFsWriteContractResponse{}, err
	}
	var result model.FsWriteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return agentcompatFsWriteContractResponse{}, err
	}
	return agentcompatFsWriteContractResponse{Result: result, AgentRPCResponse: true}, nil
}

func decodeAgentcompatJSON(context *gin.Context, value any) error {
	decoder := json.NewDecoder(context.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON values are not allowed")
		}
		return err
	}
	return nil
}
