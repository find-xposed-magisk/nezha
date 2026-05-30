package controller

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// 撤销发生在 register 之前时，迟到的连接必须被立即取消，而不是存活下来。
func TestRegisterAfterRevokeCancelsImmediately(t *testing.T) {
	r := newPATConnectionRegistry()
	r.revokeToken(42)

	var cancelled atomic.Bool
	dereg := r.register(42, func() { cancelled.Store(true) })

	require.True(t, cancelled.Load(), "late registration on a revoked token must cancel at once")
	require.Equal(t, 0, r.countForToken(42), "revoked token must not retain connections")
	dereg() // must be a safe no-op
}

// 并发 revoke/register 下不得有连接逃过撤销。
func TestRevokeRegisterNoSurvivor(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		r := newPATConnectionRegistry()
		var cancelled atomic.Bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.register(7, func() { cancelled.Store(true) })
		}()
		go func() {
			defer wg.Done()
			r.revokeToken(7)
		}()
		wg.Wait()

		// 无论谁先跑：要么 register 先（被 revoke 取消），要么 revoke 先
		// （register 在 tombstone 上立即取消）。两种顺序都不能留下活连接。
		require.True(t, cancelled.Load(), "iter %d: connection survived revocation", iter)
		require.Equal(t, 0, r.countForToken(7), "iter %d: registry must be empty after revoke", iter)
	}
}
