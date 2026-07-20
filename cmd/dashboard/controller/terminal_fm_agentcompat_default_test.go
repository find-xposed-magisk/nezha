//go:build !agentcompat

package controller

import (
	"errors"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestDefaultCreateTerminalPreservesCapabilityHeaderAndLegacyDispatch(t *testing.T) {
	// Given
	handler, _, request := newDefaultCreateFixture(t, "POST", "/terminal", model.TerminalForm{ServerID: 7})
	request.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, "malformed")
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	stream := &failingRequestTaskStream{err: errors.New("stop after dispatch")}
	server.SetTaskStream(stream)

	// When
	_, err := createTerminal(request)

	// Then
	require.ErrorIs(t, err, stream.err)
	require.Equal(t, "malformed", request.Request.Header.Get(agentcompatcontract.IOStreamCapabilityHeader))
	require.Equal(t, 1, stream.calls())
	require.Equal(t, 0, handler.StreamCount())
}

func TestDefaultCreateFMPreservesCapabilityHeaderAndLegacyDispatch(t *testing.T) {
	// Given
	handler, _, request := newDefaultCreateFixture(t, "POST", "/file?id=7", nil)
	request.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, "malformed")
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	stream := &failingRequestTaskStream{err: errors.New("stop after dispatch")}
	server.SetTaskStream(stream)

	// When
	_, err := createFM(request)

	// Then
	require.ErrorIs(t, err, stream.err)
	require.Equal(t, "malformed", request.Request.Header.Get(agentcompatcontract.IOStreamCapabilityHeader))
	require.Equal(t, 1, stream.calls())
	require.Equal(t, 0, handler.StreamCount())
}

func newDefaultCreateFixture(t *testing.T, method, target string, body any) (*rpc.NezhaHandler, *model.APIToken, *gin.Context) {
	t.Helper()
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	handler := rpc.NewNezhaHandler()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = handler
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	token, _ := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	request := newAuthorizedControllerContext(t, method, target, body)
	request.Set(apiTokenCtxKey, token)
	request.Set(model.CtxKeyAPIToken, token)
	return handler, token, request
}
