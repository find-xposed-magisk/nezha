//go:build linux

package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHostVersionEvidence_IsNotObserved_whenReportedVersionIsEmpty(t *testing.T) {
	// Given
	host := json.RawMessage(`{"version":""}`)

	// When
	version, observed, err := decodeHostVersionEvidence(host)

	// Then
	require.NoError(t, err)
	require.Empty(t, version)
	require.False(t, observed)
}

func TestHostVersionEvidence_IsObserved_whenReportedVersionIsNonempty(t *testing.T) {
	// Given
	host := json.RawMessage(`{"version":"v2.1.0"}`)

	// When
	version, observed, err := decodeHostVersionEvidence(host)

	// Then
	require.NoError(t, err)
	require.Equal(t, "v2.1.0", version)
	require.True(t, observed)
}
