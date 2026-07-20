//go:build linux && agentcompat

package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServerGetResult_VerifiesListedServerIdentity_whenUUIDOrIDMismatch(t *testing.T) {
	listed := serverListItem{ID: 81, UUID: "00000000-0000-0000-0000-000000000081", Online: true}
	tests := []struct {
		name   string
		result serverGetResult
	}{
		{
			name:   "UUID differs",
			result: serverGetResult{ID: listed.ID, UUID: "00000000-0000-0000-0000-000000000082", Host: json.RawMessage(`{}`), State: json.RawMessage(`{}`)},
		},
		{
			name:   "ID differs",
			result: serverGetResult{ID: 82, UUID: listed.UUID, Host: json.RawMessage(`{}`), State: json.RawMessage(`{}`)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.result
			err := verifyServerGetResult(listed, result)
			require.Error(t, err)
		})
	}
}

func TestServerGetResult_RejectsZeroListedServerID_whenReturnedIdentityMatches(t *testing.T) {
	// Given
	listed := serverListItem{UUID: "00000000-0000-0000-0000-000000000081", Online: true}
	result := serverGetResult{UUID: listed.UUID, Host: json.RawMessage(`{}`), State: json.RawMessage(`{}`)}

	// When
	err := verifyServerGetResult(listed, result)

	// Then
	require.Error(t, err)
	require.EqualError(t, err, "dashboard server.get identity does not match server.list")
}
