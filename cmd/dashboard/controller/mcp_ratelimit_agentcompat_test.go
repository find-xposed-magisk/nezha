//go:build agentcompat

package controller

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAgentcompatMCPRateLimitProbe_returnsTypedBoundaryCountsWithoutSharedState(t *testing.T) {
	// Given
	request := agentcompatMCPRateLimitProbeRequest{}
	originalLimiter := mcpRateLimiterShared

	// When
	response, err := runAgentcompatMCPRateLimitProbe(request)

	// Then
	if err != nil {
		t.Fatalf("probe returned error: %v", err)
	}
	if response.SecondAllowedCount != 10 || response.SecondRejectedAtCount != 11 || response.MinuteAllowedCount != 120 || response.MinuteRejectedAtCount != 121 {
		t.Fatalf("probe result = %+v, want second=10/11 minute=120/121", response)
	}
	t.Logf("typed probe result: second=%d/%d minute=%d/%d", response.SecondAllowedCount, response.SecondRejectedAtCount, response.MinuteAllowedCount, response.MinuteRejectedAtCount)
	if mcpRateLimiterShared != originalLimiter {
		t.Fatal("probe mutated the shared production limiter")
	}
}

func TestAgentcompatMCPRateLimitProbe_isRepeatableAndConcurrentSafe(t *testing.T) {
	// Given
	request := agentcompatMCPRateLimitProbeRequest{}
	results := make(chan agentcompatMCPRateLimitProbeResponse, 8)
	errors := make(chan error, 8)

	// When
	var waitGroup sync.WaitGroup
	for probeNumber := 0; probeNumber < 8; probeNumber++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			response, err := runAgentcompatMCPRateLimitProbe(request)
			if err != nil {
				errors <- err
				return
			}
			results <- response
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errors)

	// Then
	for err := range errors {
		t.Fatalf("concurrent probe returned error: %v", err)
	}
	for response := range results {
		if response.SecondAllowedCount != 10 || response.SecondRejectedAtCount != 11 || response.MinuteAllowedCount != 120 || response.MinuteRejectedAtCount != 121 {
			t.Fatalf("concurrent probe result = %+v, want second=10/11 minute=120/121", response)
		}
	}
}

func TestAgentcompatMCPRateLimitProbe_rejectsCallerControlledParameters(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"token_id":7}`))
	context.Request.Header.Set("Content-Type", "application/json")

	// When
	var request agentcompatMCPRateLimitProbeRequest
	err := decodeAgentcompatJSON(context, &request)

	// Then
	if err == nil {
		t.Fatal("caller-controlled rate probe parameters must be rejected")
	}
	context.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{}`))
	if err := decodeAgentcompatJSON(context, &request); err != nil {
		t.Fatalf("canonical empty probe request must be accepted: %v", err)
	}
	if _, err := json.Marshal(request); err != nil {
		t.Fatalf("canonical request must remain JSON encodable: %v", err)
	}
}

func TestAgentcompatMCPRateLimitProbeRejectsTrailingJSON(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{}{}`))
	var request agentcompatMCPRateLimitProbeRequest
	if err := decodeAgentcompatJSON(context, &request); err == nil {
		t.Fatal("trailing JSON values must be rejected")
	}
}
