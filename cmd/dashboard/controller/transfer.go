package controller

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// List server transfers
// @Summary List server transfers
// @Security BearerAuth
// @Schemes
// @Description Returns transfers visible to the caller. Admin sees all; a
// @Description member sees rows where they are FromUserID, ToUserID, or
// @Description InitiatorID. The same predicate is enforced both at the SQL
// @Description level (this handler) and by the listHandler post-filter
// @Description (ServerTransfer.HasPermission) — defence in depth.
// @Tags auth required
// @Produce json
// @Success 200 {object} model.CommonResponse[[]model.ServerTransfer]
// @Router /transfer [get]
func listServerTransfer(c *gin.Context) ([]*model.ServerTransfer, error) {
	q := singleton.DB.Order("id DESC")
	// ServerTransfer is the only listX endpoint that hits the DB — the others
	// all serve in-memory caches — and it is an append-only audit log. Without
	// this SQL-side filter, every member's page load scans the entire historical
	// population only to have the listHandler post-filter throw most of it
	// away. As the table grows (a single transfer per server-move adds a row
	// forever) this degrades from cheap to dashboard-blocking. Mirror the
	// HasPermission predicate at the WHERE clause for non-admins. The post-filter
	// still runs unconditionally as a defence-in-depth guard.
	if !callerIsAdmin(c) {
		uid := getUid(c)
		q = q.Where("from_user_id = ? OR to_user_id = ? OR initiator_id = ?", uid, uid, uid)
	}
	var transfers []*model.ServerTransfer
	if err := q.Find(&transfers).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return transfers, nil
}

// Cancel server transfer
// @Summary Cancel server transfer
// @Security BearerAuth
// @Schemes
// @Description Cancels a Pending transfer and reverts Server.UserID back to
// @Description FromUserID. Only admin or the original FromUserID may cancel
// @Description (the new owner cannot — that would be a denial primitive
// @Description against a server they don't own yet). No-op if the transfer is
// @Description already terminal.
// @Tags auth required
// @Param id path uint true "Transfer ID"
// @Produce json
// @Success 200 {object} model.CommonResponse[model.ServerTransfer]
// @Router /transfer/{id}/cancel [post]
func cancelServerTransfer(c *gin.Context) (*model.ServerTransfer, error) {
	tid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return nil, err
	}

	// Avoid leaking transfer-row existence via response shape. Admin can
	// look up any row; a member can only look up rows where they are the
	// FromUserID. Both "row does not exist" and "row exists but caller is
	// not FromUserID" must surface identically as permission denied.
	q := singleton.DB
	if !callerIsAdmin(c) {
		q = q.Where("from_user_id = ?", getUid(c))
	}
	var t model.ServerTransfer
	if err := q.First(&t, tid).Error; err != nil {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	if !t.HasPermission(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	updated, err := singleton.ServerTransferShared.Cancel(tid)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		// Already terminal — return current state so the UI can refresh.
		return &t, nil
	}
	return updated, nil
}

// Retry server transfer
// @Summary Retry server transfer
// @Security BearerAuth
// @Schemes
// @Description Creates a fresh Pending transfer with the same From/To as a
// @Description terminal (Failed/Timeout/Cancelled) transfer. The previous row
// @Description is left intact for audit; this returns the new row. Admin-only:
// @Description non-admin transfer semantics are enforced by batchMoveServer's
// @Description "ToUser == self" rule, so allowing any historical From/To/
// @Description Initiator to retry would reintroduce the give-away path. Non-
// @Description admins receive permission denied before the transfer row is
// @Description read so the response cannot enumerate transfer ids.
// @Tags auth required
// @Param id path uint true "Transfer ID"
// @Produce json
// @Success 200 {object} model.CommonResponse[model.ServerTransfer]
// @Router /transfer/{id}/retry [post]
func retryServerTransfer(c *gin.Context) (*model.ServerTransfer, error) {
	// Retry is admin-only. Non-admin transfer semantics are enforced by
	// batchMoveServer's "ToUser == self" rule, which means a member can only
	// receive a server, never give one away. Allowing a member to retry a
	// historical row would reintroduce the give-away path: any prev.ToUserID
	// on file becomes a one-click bypass of that policy. Members who want
	// the server moved elsewhere ask an admin or use batch-move to pull it
	// onto themselves.
	//
	// Refuse non-admins before reading the row so the response cannot be
	// used to enumerate which transfer ids exist.
	if !callerIsAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	tid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return nil, err
	}

	var prev model.ServerTransfer
	if err := singleton.DB.First(&prev, tid).Error; err != nil {
		return nil, newGormError("%v", err)
	}

	// PAT server_ids 白名单必须在 admin short-circuit 之后再收一次，否则
	// admin 给自己签发的“仅 server_ids={X}”PAT 仍能 retry 任意历史 transfer
	// 行，与 ServerTransfer.HasPermission 注释和 cancelServerTransfer 已有
	// 的复核语义直接冲突。
	if !prev.HasPermission(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	return singleton.ServerTransferShared.Retry(&prev, getUid(c))
}

// transferStreamWriteTimeout caps a single WriteMessage so a stuck or
// half-open client cannot block the broker fan-out forever — once exceeded
// the connection is considered dead and dropped. Matches the cadence of the
// keepalive ping below.
const transferStreamWriteTimeout = 10 * time.Second

// transferStreamPingInterval is how often we send a ping to keep the
// connection alive through aggressive proxies. Independent of the event
// stream, so silent transfers still keep the socket warm.
const transferStreamPingInterval = 30 * time.Second

// Websocket server transfer stream
// @Summary Websocket server transfer stream
// @Security BearerAuth
// @Schemes
// @Description Pushes ServerTransfer state transitions (Pending → Verified /
// @Description Failed / Timeout / Cancelled) to the dashboard so the UI can
// @Description react without polling. Each frame is a single JSON-encoded
// @Description ServerTransfer. Subscribers see only transfers visible to
// @Description them (ServerTransfer.HasPermission).
// @tags common
// @Produce json
// @Success 200 {object} model.ServerTransfer
// @Router /ws/transfer [get]
func transferStream(c *gin.Context) (any, error) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return nil, newWsError("%v", err)
	}
	defer conn.Close()

	deregisterPAT := registerPATConnection(c, func() { _ = conn.Close() })
	defer deregisterPAT()

	subID, ch := singleton.ServerTransferShared.Subscribe()
	defer singleton.ServerTransferShared.Unsubscribe(subID)

	// Pings keep the socket warm even when the broker is quiet. Without this
	// a long idle period followed by a transfer event would race against
	// upstream proxy idle-timeouts that may have already closed the conn.
	ping := time.NewTicker(transferStreamPingInterval)
	defer ping.Stop()

	// Reader goroutine: needed only to surface client disconnects through
	// SetReadDeadline / ReadMessage. We never expect inbound payloads.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-closed:
			return nil, newWsError("")
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(transferStreamWriteTimeout)); err != nil {
				return nil, newWsError("%v", err)
			}
		case t, ok := <-ch:
			if !ok {
				return nil, newWsError("")
			}
			if !t.HasPermission(c) {
				continue
			}
			payload, err := json.Marshal(t)
			if err != nil {
				continue
			}
			if err := conn.SetWriteDeadline(time.Now().Add(transferStreamWriteTimeout)); err != nil {
				return nil, newWsError("%v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return nil, newWsError("%v", err)
			}
		}
	}
}
