package controller

import (
	"sync"
	"time"

	"github.com/nezhahq/nezha/model"
)

// revokeTombstoneTTL bounds how long a revoked token id is remembered to
// close the revoke->register race. The race window is a single request's
// auth-to-register gap (sub-second); minutes of slack is ample. Without a
// TTL the tombstone set grows unbounded over the process lifetime as PATs
// are created and deleted.
const revokeTombstoneTTL = 10 * time.Minute

// patConnectionRegistry tracks active long-lived connections (terminal,
// FM, ws/server, ws/transfer, etc.) per PAT id so that deleteAPIToken can
// cancel them immediately on revocation. Without this, a deleted PAT
// keeps streaming until the underlying connection naturally drops.
//
// The registry deliberately holds no goroutines — it only stores cancel
// hooks the connection setup already owns. Handlers register on entry
// and deregister on exit; revokeToken walks the per-token slice and
// invokes every hook under the lock.
type patConnectionRegistry struct {
	mu      sync.Mutex
	byToken map[uint64]map[uint64]func()
	// revoked is a tombstone set closing the revoke->register race: a
	// connection can pass apiTokenAuthMiddleware (token cached in ctx) and
	// only register its cancel hook AFTER deleteAPIToken already walked the
	// registry. Without the tombstone that late registration would survive
	// revocation. register consults revoked under the same lock and cancels
	// immediately when the id is already gone.
	revoked map[uint64]time.Time
	nextID  uint64
}

func newPATConnectionRegistry() *patConnectionRegistry {
	return &patConnectionRegistry{
		byToken: make(map[uint64]map[uint64]func()),
		revoked: make(map[uint64]time.Time),
	}
}

// pruneRevokedLocked drops tombstones older than revokeTombstoneTTL. Caller
// must hold r.mu. Bounds the tombstone set to recently-revoked ids.
func (r *patConnectionRegistry) pruneRevokedLocked(now time.Time) {
	for id, at := range r.revoked {
		if now.Sub(at) > revokeTombstoneTTL {
			delete(r.revoked, id)
		}
	}
}

// register stores cancel under tokenID and returns a deregister hook the
// caller MUST invoke when the connection ends. Returning a closure
// (rather than exposing an id) prevents callers from forgetting to clean
// up and avoids leaking entries past connection lifetime.
//
// If tokenID was already revoked, register does NOT store the hook; it
// cancels immediately and returns a no-op deregister, so a connection that
// raced past revocation is torn down at once.
func (r *patConnectionRegistry) register(tokenID uint64, cancel func()) func() {
	r.mu.Lock()
	now := time.Now()
	r.pruneRevokedLocked(now)
	if at, dead := r.revoked[tokenID]; dead && now.Sub(at) <= revokeTombstoneTTL {
		r.mu.Unlock()
		cancel()
		return func() {}
	}
	r.nextID++
	id := r.nextID
	conns, ok := r.byToken[tokenID]
	if !ok {
		conns = make(map[uint64]func())
		r.byToken[tokenID] = conns
	}
	conns[id] = cancel
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if m, ok := r.byToken[tokenID]; ok {
				delete(m, id)
				if len(m) == 0 {
					delete(r.byToken, tokenID)
				}
			}
		})
	}
}

// revokeToken cancels every active connection registered under tokenID,
// clears the entry, and records a tombstone so any connection still racing
// toward register is cancelled on arrival. Safe to call on an unknown id.
func (r *patConnectionRegistry) revokeToken(tokenID uint64) {
	r.mu.Lock()
	conns := r.byToken[tokenID]
	delete(r.byToken, tokenID)
	now := time.Now()
	r.pruneRevokedLocked(now)
	r.revoked[tokenID] = now
	r.mu.Unlock()

	for _, cancel := range conns {
		cancel()
	}
}

// countForToken returns the number of active connections registered
// under tokenID. Intended for tests + future SIEM exposure; callers MUST
// NOT use it for policy decisions because the count can change the
// instant the lock is released.
func (r *patConnectionRegistry) countForToken(tokenID uint64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byToken[tokenID])
}

var patConnectionRegistryShared = newPATConnectionRegistry()

// registerPATConnection wires the request-bound PAT (if any) into the
// process-wide revocation registry. Returns a deregister hook the
// handler MUST defer. For JWT-authenticated requests the hook is a
// no-op so call sites stay portable.
//
// Long-lived endpoints (terminal, FM, ws/server, ws/transfer) call
// this on entry and pass a cancel function that drops their websocket
// or relay loop. deleteAPIToken then revokes every active hook
// registered under the deleted token id.
func registerPATConnection(c interface {
	Get(any) (any, bool)
}, cancel func()) func() {
	v, ok := c.Get(apiTokenCtxKey)
	if !ok {
		return func() {}
	}
	tok, ok := v.(*model.APIToken)
	if !ok || tok == nil {
		return func() {}
	}
	return patConnectionRegistryShared.register(tok.ID, cancel)
}
