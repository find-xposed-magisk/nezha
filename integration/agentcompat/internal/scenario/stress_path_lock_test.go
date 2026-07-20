//go:build linux && agentcompat

package scenario

import (
	"crypto/sha256"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStressPathLockProofRequiresExactStripeCount(t *testing.T) {
	for _, stripes := range []int{1023, 1025} {
		require.ErrorIs(t, (stressPathLockProof{Stripes: stripes}).Validate(), ErrStressPathLockProof)
	}
	require.NoError(t, (stressPathLockProof{Stripes: 1024}).Validate())
}

func TestStressPathLockProofDoesNotModifyAgentSource(t *testing.T) {
	sourceDir := os.Getenv("AGENTCOMPAT_AGENT_SOURCE")
	if sourceDir == "" {
		t.Skip("AGENTCOMPAT_AGENT_SOURCE is not configured")
	}
	path := sourceDir + "/cmd/agent/mcp_fs_path_lock.go"
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	beforeHash := sha256.Sum256(before)
	proof, err := proveStressPathLockStripes(t.Context(), sourceDir)
	require.NoError(t, err)
	require.NoError(t, proof.Validate())
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, beforeHash, sha256.Sum256(after))
}
