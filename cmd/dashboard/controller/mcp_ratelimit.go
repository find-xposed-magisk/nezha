package controller

import (
	"sync"
	"time"
)

// MCPRateLimiter 实现按 token 的双层 token bucket：
//   - 秒级：默认 10 req/s，应对单个 LLM 突发
//   - 分钟级：默认 120 req/min，应对长时间刷
//
// 实现走简单 sliding window（按桶截断的 counter），轻量、O(1)；
// 进程内即可，未持久化——重启等价于配额刷新，可接受。
type MCPRateLimiter struct {
	mu        sync.Mutex
	perToken  map[uint64]*tokenWindow
	secLimit  int
	minLimit  int
	lastPrune time.Time
}

type tokenWindow struct {
	secBucketStart time.Time
	secCount       int
	minBucketStart time.Time
	minCount       int
}

// mcpRateLimiterPruneInterval bounds how often Allow sweeps the map. Without
// eviction the map kept one entry per token ID ever seen, so PAT churn grew
// it without bound. A token idle longer than its minute bucket carries no
// live budget, so dropping it is lossless; the interval keeps the sweep
// amortized O(1) per call instead of O(map) every call.
const mcpRateLimiterPruneInterval = time.Minute

func newMCPRateLimiter(secLimit, minLimit int) *MCPRateLimiter {
	return &MCPRateLimiter{
		perToken: make(map[uint64]*tokenWindow),
		secLimit: secLimit,
		minLimit: minLimit,
	}
}

// pruneStaleLocked drops windows whose minute bucket started more than one
// minute ago: such a token has no accumulated budget left, so removing it
// cannot change any future Allow decision. Caller must hold r.mu.
func (r *MCPRateLimiter) pruneStaleLocked(now time.Time) {
	for id, w := range r.perToken {
		if now.Sub(w.minBucketStart) >= time.Minute {
			delete(r.perToken, id)
		}
	}
}

// Allow 返回是否允许本次调用。被拒返回 false。
// tokenID = 0 时不限流（管理路径或匿名）。
func (r *MCPRateLimiter) Allow(tokenID uint64) bool {
	if tokenID == 0 {
		return true
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.lastPrune) >= mcpRateLimiterPruneInterval {
		r.pruneStaleLocked(now)
		r.lastPrune = now
	}
	w, ok := r.perToken[tokenID]
	if !ok {
		w = &tokenWindow{secBucketStart: now, minBucketStart: now}
		r.perToken[tokenID] = w
	}
	if now.Sub(w.secBucketStart) >= time.Second {
		w.secBucketStart = now
		w.secCount = 0
	}
	if now.Sub(w.minBucketStart) >= time.Minute {
		w.minBucketStart = now
		w.minCount = 0
	}
	if w.secCount >= r.secLimit || w.minCount >= r.minLimit {
		return false
	}
	w.secCount++
	w.minCount++
	return true
}

// 全局单例，参数固定（生产可观测后再考虑配置化）。
var mcpRateLimiterShared = newMCPRateLimiter(10, 120)
