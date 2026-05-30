package model

import "github.com/gin-gonic/gin"

type NAT struct {
	Common
	Enabled  bool   `json:"enabled"`
	Name     string `json:"name"`
	ServerID uint64 `json:"server_id"`
	Host     string `json:"host"`
	Domain   string `json:"domain" gorm:"unique"`
}

// HasPermission 在 owner/admin 之上叠加 PAT 的 server_ids 白名单，
// 与 Server/Service/Cron.HasPermission 一致，避免 server-limited PAT 越权。
func (n *NAT) HasPermission(ctx *gin.Context) bool {
	if !n.Common.HasPermission(ctx) {
		return false
	}
	v, ok := ctx.Get(CtxKeyAPIToken)
	if !ok {
		return true
	}
	tok, ok := v.(APITokenAccessor)
	if !ok || tok == nil {
		return true
	}
	return tok.CanAccessServer(n.ServerID)
}
