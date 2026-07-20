//go:build agentcompat && linux

package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/service/singleton"
)

func TestAgentcompatSQLiteHoldErrorUsesFixedRedactedMessages(t *testing.T) {
	tests := []struct {
		cause error
		want  string
	}{
		{agentcompatSQLiteHoldInvalidRequest{}, "agentcompat sqlite hold request is invalid"},
		{singleton.ErrSQLiteHoldSessionActive, "agentcompat sqlite hold is active"},
		{singleton.ErrSQLiteHoldStaleSession, "agentcompat sqlite hold receipt is stale"},
		{singleton.ErrSQLiteHoldFinalizationNotStarted, "agentcompat sqlite hold is not ready"},
		{singleton.ErrSQLiteHoldFinalizationStarted, "agentcompat sqlite hold is not ready"},
		{singleton.ErrSQLiteHoldUnexpectedSelection, "agentcompat sqlite hold was aborted"},
		{singleton.ErrSQLiteHoldAmbiguousCandidate, "agentcompat sqlite hold was aborted"},
		{singleton.ErrSQLiteHoldAborted, "agentcompat sqlite hold was aborted"},
		{context.Canceled, "agentcompat sqlite hold wait was canceled"},
		{context.DeadlineExceeded, "agentcompat sqlite hold wait was canceled"},
		{errors.New("path=/secret token=nzp_secret"), "agentcompat sqlite hold control is unavailable"},
	}

	for _, test := range tests {
		// When
		err := agentcompatSQLiteHoldError(test.cause)

		// Then
		require.EqualError(t, err, test.want)
	}
}
