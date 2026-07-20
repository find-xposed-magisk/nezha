//go:build agentcompat

package controller

import (
	"time"

	"github.com/gin-gonic/gin"
)

type agentcompatMCPRateLimitProbeRequest struct {
}

type agentcompatMCPRateLimitProbeResponse struct {
	SecondAllowedCount    int `json:"second_allowed_count"`
	SecondRejectedAtCount int `json:"second_rejected_at_count"`
	MinuteAllowedCount    int `json:"minute_allowed_count"`
	MinuteRejectedAtCount int `json:"minute_rejected_at_count"`
}

func agentcompatMCPRateLimitProbeRoute(context *gin.Context) (agentcompatMCPRateLimitProbeResponse, error) {
	var request agentcompatMCPRateLimitProbeRequest
	if err := decodeAgentcompatJSON(context, &request); err != nil {
		return agentcompatMCPRateLimitProbeResponse{}, err
	}
	return runAgentcompatMCPRateLimitProbe(request)
}

func runAgentcompatMCPRateLimitProbe(request agentcompatMCPRateLimitProbeRequest) (agentcompatMCPRateLimitProbeResponse, error) {
	response := agentcompatMCPRateLimitProbeResponse{}
	secondNow := time.Unix(1_700_000_000, 0)
	secondLimiter := newMCPRateLimiterWithClock(10, 120, func() time.Time { return secondNow })
	for requestNumber := 1; requestNumber <= 11; requestNumber++ {
		if secondLimiter.Allow(1) {
			response.SecondAllowedCount++
		} else if response.SecondRejectedAtCount == 0 {
			response.SecondRejectedAtCount = requestNumber
		}
	}
	minuteNow := time.Unix(1_700_000_000, 0)
	minuteLimiter := newMCPRateLimiterWithClock(10_000, 120, func() time.Time { return minuteNow })
	for requestNumber := 1; requestNumber <= 121; requestNumber++ {
		if minuteLimiter.Allow(1) {
			response.MinuteAllowedCount++
		} else if response.MinuteRejectedAtCount == 0 {
			response.MinuteRejectedAtCount = requestNumber
		}
		minuteNow = minuteNow.Add(100 * time.Millisecond)
	}
	return response, nil
}
