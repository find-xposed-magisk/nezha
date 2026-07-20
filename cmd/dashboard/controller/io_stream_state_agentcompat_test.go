//go:build agentcompat

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/rpc"
)

func TestAgentcompatIOStreamStateRoutesRequirePATAndReturnTypedState(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	defer cleanup()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeInventoryRead}, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAgentcompatRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	unauthenticated, err := http.NewRequest(http.MethodGet, server.URL+"/agentcompat/io-stream-state", nil)
	require.NoError(t, err)
	response, err := server.Client().Do(unauthenticated)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	response.Body.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/agentcompat/io-stream-state", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	var envelope model.CommonResponse[rpc.IOStreamState]
	responseBody, readErr := io.ReadAll(response.Body)
	require.NoError(t, readErr)
	require.NotContains(t, string(responseBody), "route-wait")
	require.NoError(t, json.Unmarshal(responseBody, &envelope))
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.True(t, envelope.Success)
	require.Equal(t, rpc.IOStreamState{}, envelope.Data)
}

func TestAgentcompatIOStreamStateWaitRouteWakesAfterClose(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	defer cleanup()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeInventoryRead}, nil)
	require.NoError(t, rpc.NezhaHandlerSingleton.CreateStream("route-wait", 1, 7))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAgentcompatRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	body, err := json.Marshal(rpc.IOStreamStateExpectation{ExpectedCount: rpc.ExpectedIOStreamCount(0), AbsentStreamID: "route-wait"})
	require.NoError(t, err)
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, server.URL+"/agentcompat/io-stream-state", bytes.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	result := make(chan *http.Response, 1)
	go func() {
		response, requestErr := server.Client().Do(request)
		if requestErr == nil {
			result <- response
		}
	}()
	rpc.NezhaHandlerSingleton.CloseStream("route-wait")
	response := <-result
	defer response.Body.Close()
	var envelope model.CommonResponse[rpc.IOStreamState]
	responseBody, readErr := io.ReadAll(response.Body)
	require.NoError(t, readErr)
	require.NotContains(t, string(responseBody), "route-wait")
	require.NoError(t, json.Unmarshal(responseBody, &envelope))
	require.True(t, envelope.Success)
	require.Equal(t, 0, envelope.Data.Count)
	require.Equal(t, uint64(2), envelope.Data.Generation)
}

func TestAgentcompatIOStreamStateCreateWaitRouteWakesAfterCreate(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	defer cleanup()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeInventoryRead}, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	waitCaptured := make(chan struct{})
	var observerOnce sync.Once
	rpc.NezhaHandlerSingleton.SetIOStreamStateWaitObserverForAgentcompat(func() {
		observerOnce.Do(func() { close(waitCaptured) })
	})
	t.Cleanup(func() {
		rpc.NezhaHandlerSingleton.SetIOStreamStateWaitObserverForAgentcompat(nil)
	})
	registerAgentcompatRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	body := bytes.NewBufferString(`{"expected_count":1}`)
	requestContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, server.URL+"/agentcompat/io-stream-state", body)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	result := make(chan *http.Response, 1)
	go func() {
		response, requestErr := server.Client().Do(request)
		if requestErr == nil {
			result <- response
		}
	}()
	select {
	case <-waitCaptured:
	case <-requestContext.Done():
		t.Fatal(requestContext.Err())
	}
	require.NoError(t, rpc.NezhaHandlerSingleton.CreateStream("route-create", 1, 7))
	var response *http.Response
	select {
	case response = <-result:
	case <-requestContext.Done():
		t.Fatal(requestContext.Err())
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	var envelope model.CommonResponse[rpc.IOStreamState]
	require.NoError(t, json.Unmarshal(responseBody, &envelope))
	require.True(t, envelope.Success)
	require.Equal(t, 1, envelope.Data.Count)
	require.Equal(t, uint64(1), envelope.Data.Generation)
	require.NotContains(t, string(responseBody), "route-create")
}

func TestAgentcompatIOStreamStateRejectsInvalidExpectationAndCancellation(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	defer cleanup()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeInventoryRead}, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAgentcompatRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	for _, rawBody := range []string{`{}`, `{"expected_count":null}`, `{"expected_count":-1,"absent_stream_id":"private-stream-id"}`} {
		body := bytes.NewBufferString(rawBody)
		request, err := http.NewRequest(http.MethodPost, server.URL+"/agentcompat/io-stream-state", body)
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		require.NoError(t, err)
		responseBody, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		require.NoError(t, readErr)
		var envelope model.CommonResponse[rpc.IOStreamState]
		require.NoError(t, json.Unmarshal(responseBody, &envelope))
		require.False(t, envelope.Success)
		require.NotEmpty(t, envelope.Error)
		require.NotContains(t, string(responseBody), "private-stream-id")
	}

	body := bytes.NewBufferString(`{"absent_stream_id":"absence-only"}`)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/agentcompat/io-stream-state", body)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	var envelope model.CommonResponse[rpc.IOStreamState]
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	require.True(t, envelope.Success)
	require.Equal(t, 0, envelope.Data.Count)

	unauthenticatedBody := bytes.NewBufferString(`{"expected_count":0}`)
	unauthenticatedPost, err := http.NewRequest(http.MethodPost, server.URL+"/agentcompat/io-stream-state", unauthenticatedBody)
	require.NoError(t, err)
	unauthenticatedPost.Header.Set("Content-Type", "application/json")
	unauthenticatedResponse, err := server.Client().Do(unauthenticatedPost)
	require.NoError(t, err)
	unauthenticatedResponse.Body.Close()
	require.Equal(t, http.StatusUnauthorized, unauthenticatedResponse.StatusCode)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, waitErr := rpc.NezhaHandlerSingleton.WaitForIOStreamState(ctx, rpc.IOStreamStateExpectation{ExpectedCount: rpc.ExpectedIOStreamCount(1)})
	require.True(t, errors.Is(waitErr, context.Canceled))
}
