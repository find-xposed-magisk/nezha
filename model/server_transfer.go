package model

import (
	"time"

	"github.com/gin-gonic/gin"
)

// ServerTransferStatus represents the lifecycle state of a server ownership
// transfer. A transfer's life starts at Pending (server.user_id has been
// flipped to the new owner; agent still authenticates with the old owner's
// AgentSecret) and ends in exactly one of the terminal states.
type ServerTransferStatus uint8

const (
	// ServerTransferStatusPending means the dashboard has flipped Server.UserID
	// to the new owner and queued an ApplyConfig task to swap the agent's
	// client_secret. Auth still accepts the old owner's AgentSecret for this
	// UUID until verification arrives or the transfer times out.
	ServerTransferStatusPending ServerTransferStatus = iota
	// ServerTransferStatusVerified means the agent successfully reconnected
	// using the new owner's AgentSecret. Auth no longer tolerates the old
	// owner's secret on this UUID.
	ServerTransferStatusVerified
	// ServerTransferStatusFailed means the agent explicitly reported the
	// ApplyConfig task as unsuccessful (e.g. DisableCommandExecute). The
	// dashboard has rolled Server.UserID back to FromUserID.
	ServerTransferStatusFailed
	// ServerTransferStatusTimeout means the verification window expired
	// without the agent reconnecting under the new secret. The dashboard has
	// rolled Server.UserID back to FromUserID.
	ServerTransferStatusTimeout
	// ServerTransferStatusCancelled means an administrator cancelled the
	// transfer before any verification event was observed. The dashboard has
	// rolled Server.UserID back to FromUserID.
	ServerTransferStatusCancelled
)

// IsTerminal reports whether the status represents a settled transfer. Only
// terminal transfers are eligible for retry and they will never be in the
// pending index.
func (s ServerTransferStatus) IsTerminal() bool {
	return s != ServerTransferStatusPending
}

// ServerTransfer records a single attempt to transfer ownership of one server
// to another user. It is the source of truth for the auth-tolerance window
// during a transfer — service/rpc.authorizeAgentForUUID consults the pending
// index built from this table to decide whether to accept the old owner's
// AgentSecret on the affected UUID.
//
// Naming note: the existing model.Transfer records hourly traffic snapshots
// and is unrelated. This entity is named ServerTransfer to disambiguate.
type ServerTransfer struct {
	Common
	ServerID    uint64               `json:"server_id" gorm:"index"`
	FromUserID  uint64               `json:"from_user_id"`
	ToUserID    uint64               `json:"to_user_id"`
	InitiatorID uint64               `json:"initiator_id"`
	Status      ServerTransferStatus `json:"status" gorm:"index"`
	LastError   string               `json:"last_error,omitempty"`
	AckedAt     *time.Time           `json:"acked_at,omitempty"`
	// HandshakeSecret is a per-transfer random credential that PushIfOnline
	// delivers in place of the destination user's global AgentSecret. The
	// agent treats it as a temporary handshake token: it rotates to this
	// secret on the 10s reload, reconnects, and the dashboard's auth path
	// recognises it as proof of transfer delivery (MarkVerified). It is
	// scoped to this single transfer and to this single UUID — leaking it
	// to the previous owner who hijacks the stream still does NOT expose
	// the destination user's other agents. Never returned to API clients.
	HandshakeSecret string `json:"-" gorm:"type:char(32)"`
	// RevertHandshakeSecret is the same idea for the rollback path: when
	// the dashboard pushes a revert ApplyConfig over a stream now held by
	// the destination user, we must not embed the source user's global
	// AgentSecret. Instead the agent rotates back through this token, which
	// is recognised by the auth path during the revert window only.
	RevertHandshakeSecret string `json:"-" gorm:"type:char(32)"`
}

// HasPermission overrides Common.HasPermission so a transfer is visible to
// admins, the source user, the destination user, and the initiator. Listing
// uses this to filter what the caller can see; mutating endpoints (cancel,
// retry) layer additional checks on top.
func (t *ServerTransfer) HasPermission(ctx *gin.Context) bool {
	auth, ok := ctx.Get(CtxKeyAuthorizedUser)
	if !ok {
		return false
	}
	user := *auth.(*User)
	if user.Role == RoleAdmin {
		return true
	}
	return user.ID == t.FromUserID || user.ID == t.ToUserID || user.ID == t.InitiatorID
}

// BatchMoveServerResultStatus is the per-server outcome returned by the
// batch-move endpoint. It maps to TransferStatus for transfers that were
// successfully created, plus extra synchronous-failure modes (permission,
// duplicate active transfer, missing server) that never produce a row.
type BatchMoveServerResultStatus string

const (
	// BatchMoveServerResultPending: ServerTransfer row created, agent push
	// in progress. Callers should watch the WS for terminal status.
	BatchMoveServerResultPending BatchMoveServerResultStatus = "pending"
	// BatchMoveServerResultPermissionDenied: caller cannot move this server.
	BatchMoveServerResultPermissionDenied BatchMoveServerResultStatus = "permission_denied"
	// BatchMoveServerResultAlreadyTransferring: server already has an in-flight
	// ServerTransfer row, cancel or wait first.
	BatchMoveServerResultAlreadyTransferring BatchMoveServerResultStatus = "already_transferring"
	// BatchMoveServerResultServerNotFound: server id does not exist.
	BatchMoveServerResultServerNotFound BatchMoveServerResultStatus = "server_not_found"
	// BatchMoveServerResultSameOwner: target user already owns this server.
	BatchMoveServerResultSameOwner BatchMoveServerResultStatus = "same_owner"
	// BatchMoveServerResultAgentTooOld: agent build does not understand
	// TaskTypeServerTransferApply, so the rotation would never complete and
	// dashboard refuses to start it. Operator must upgrade the agent.
	BatchMoveServerResultAgentTooOld BatchMoveServerResultStatus = "agent_too_old"
)

// BatchMoveServerResult is one entry in the batchMoveServer response, one
// per requested server id, in the same order.
type BatchMoveServerResult struct {
	ServerID   uint64                      `json:"server_id"`
	Status     BatchMoveServerResultStatus `json:"status"`
	TransferID uint64                      `json:"transfer_id,omitempty"`
	Error      string                      `json:"error,omitempty"`
}
