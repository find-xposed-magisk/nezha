//go:build linux && agentcompat

package agent

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreparedBinary_ConcurrentConsumersSharePathAndBlockClose(t *testing.T) {
	prepared, err := PrepareBinary(t.Context(), testAgentSourceDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = prepared.Close() })

	releases := make(chan func(), 8)
	errorsChannel := make(chan error, 8)
	var acquireGroup sync.WaitGroup
	for range 8 {
		acquireGroup.Add(1)
		go func() {
			defer acquireGroup.Done()
			path, release, acquireErr := prepared.acquire()
			if acquireErr == nil && path != prepared.BinaryPath() {
				acquireErr = fmt.Errorf("acquired path %q differs from prepared path", path)
			}
			errorsChannel <- acquireErr
			if acquireErr == nil {
				releases <- release
			}
		}()
	}
	acquireGroup.Wait()
	close(errorsChannel)
	close(releases)
	for acquireErr := range errorsChannel {
		require.NoError(t, acquireErr)
	}

	closeErr := prepared.Close()
	var usageErr *PreparedBinaryUsageError
	require.ErrorAs(t, closeErr, &usageErr)
	require.Equal(t, "has active consumers", usageErr.Reason)

	var releaseGroup sync.WaitGroup
	for release := range releases {
		releaseGroup.Add(1)
		go func() {
			defer releaseGroup.Done()
			release()
		}()
	}
	releaseGroup.Wait()
	require.NoError(t, prepared.Close())
}
