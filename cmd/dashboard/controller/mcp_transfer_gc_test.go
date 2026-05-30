package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// transferEntries 是 mint→consume 的内存表。
// mintTransferToken 把 token Store 进去，consumeTransferToken 命中后才删，
// PurgeTransferEntries 是 kill switch 的全量清理。这条路径目前缺一个
// 按 ExpiresAt 的过期回收：从未被 consume 的 token 会一直留到下一次
// kill switch 才被清掉。
//
// 这条测试钉死「过期项必须被 gcExpiredTransferEntries() 清掉，
// 且未过期项必须保留」。
func TestGCExpiredTransferEntries_RemovesOnlyExpired(t *testing.T) {
	// 隔离全局状态，避免被其它测试遗留的 entry 干扰。
	PurgeTransferEntries()
	t.Cleanup(func() { PurgeTransferEntries() })

	now := time.Now()
	expiredTok, err := mintTransferToken(transferEntry{
		UserID:    1,
		TokenID:   1,
		ServerID:  1,
		Path:      "/srv/expired",
		Direction: transferDirDownload,
		ExpiresAt: now.Add(-time.Second),
	})
	require.NoError(t, err)
	freshTok, err := mintTransferToken(transferEntry{
		UserID:    1,
		TokenID:   1,
		ServerID:  1,
		Path:      "/srv/fresh",
		Direction: transferDirDownload,
		ExpiresAt: now.Add(5 * time.Minute),
	})
	require.NoError(t, err)

	removed := gcExpiredTransferEntries(now)
	require.Equal(t, 1, removed, "exactly one expired entry must be removed")

	_, expiredStillThere := transferEntries.Load(expiredTok)
	require.False(t, expiredStillThere, "expired entry must be gone after GC")
	_, freshStillThere := transferEntries.Load(freshTok)
	require.True(t, freshStillThere, "fresh entry must survive GC")

	// 二次 GC 不应误删未过期项，也不应报告假阳性。
	require.Equal(t, 0, gcExpiredTransferEntries(now), "second GC must be a no-op for non-expired entries")
}
