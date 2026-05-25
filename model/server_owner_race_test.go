package model

import (
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// Server.UserID 在 server-transfer rotation 流程里会被 ServerTransfer 的
// Register/revertTransition 改写以反映新所有者，同时 authorizeAgentForUUID
// 在每次 agent RPC 里读取它。原实现两处都是裸字段访问，race detector 会
// 报告 data race；这是 review 评分 75 的真实问题。
//
// 修复后所有并发读写都走 SetUserID/GetUserID 的 atomic 包装，本测试在
// `go test -race` 下应该完全跑干净。
func TestServerUserIDConcurrentAccessIsRaceFree(t *testing.T) {
	s := &Server{}

	const (
		writers = 4
		readers = 8
		rounds  = 500
	)
	var wg sync.WaitGroup
	wg.Add(writers + readers)

	for i := 0; i < writers; i++ {
		uid := uint64(i + 1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				s.SetUserID(uid)
			}
		}()
	}
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				_ = s.GetUserID()
			}
		}()
	}
	wg.Wait()
}

// Common.HasPermission 是 server-transfer 旋转下与 SetUserID 并发的主要读者
// 之一：dashboard 各 controller 的 listHandler post-filter 在 transfer 窗口
// 内不断对同一 *Server 调用 HasPermission，而 Register/revertTransition 同
// 时通过 SetUserID 改写所属用户。原实现的 `user.ID == c.UserID` 是裸读，会
// 与 atomic.StoreUint64 形成 data race（go test -race 必爆）。修复后改成走
// GetUserID() 走 atomic 协议。这个测试就是用来在 -race 下钉死该不变量的。
func TestCommonHasPermissionConcurrentWithSetUserIDIsRaceFree(t *testing.T) {
	s := &Server{Common: Common{ID: 1}}

	const (
		writers = 4
		readers = 8
		rounds  = 500
	)
	var wg sync.WaitGroup
	wg.Add(writers + readers)

	for i := 0; i < writers; i++ {
		uid := uint64(i + 1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				s.SetUserID(uid)
			}
		}()
	}
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 2}, Role: RoleMember})
			for j := 0; j < rounds; j++ {
				_ = s.HasPermission(ctx)
			}
		}()
	}
	wg.Wait()
}
