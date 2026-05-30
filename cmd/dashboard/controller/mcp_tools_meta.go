package controller

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
)

// meta.whoami 让 LLM 启动时知道自己拿的是哪张 PAT、能干什么、能动哪些服务器。
// 不要求任何 scope（任意有效 PAT 均可调用）。
type whoamiResult struct {
	UserID    uint64   `json:"user_id"`
	IsAdmin   bool     `json:"is_admin"`
	TokenID   uint64   `json:"token_id"`
	TokenName string   `json:"token_name"`
	Scopes    []string `json:"scopes"`
	ServerIDs []uint64 `json:"server_ids,omitempty"`
}

func init() {
	registerMCPTool(&mcpTool{
		Name:        "meta.whoami",
		Description: "Return the identity, scopes and accessible server IDs of the current API token.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		RequiredScope: "",
		Handler:       handleMetaWhoami,
	})
}

func handleMetaWhoami(c *gin.Context, _ json.RawMessage) (any, error) {
	tok := APITokenFromContext(c)
	if tok == nil {
		return nil, errNoToken
	}
	user, _ := c.MustGet(model.CtxKeyAuthorizedUser).(*model.User)
	return whoamiResult{
		UserID:    user.ID,
		IsAdmin:   user.Role.IsAdmin(),
		TokenID:   tok.ID,
		TokenName: tok.Name,
		Scopes:    tok.Scopes(),
		ServerIDs: tok.ServerIDs(),
	}, nil
}
