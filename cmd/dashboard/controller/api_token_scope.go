package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
)

// jwtOrPATAuthMiddleware 把 PAT 与 JWT 两条鉴权链组合到 /api/v1/* 入口。
//
// 处理顺序：
//  1. apiTokenAuthMiddleware：识别 `Authorization: Bearer nzp_*`。命中（合法 PAT）
//     把 user 挂到 ctx；非法 PAT 直接 abort 401。
//  2. 如果 PAT 已挂 user → 跳过 JWT。
//  3. 否则 → JWT 中间件接管，按现有 cookie / Bearer / query token 逻辑鉴权。
//
// 存量 JWT 客户端零感知；新 PAT 客户端可直接调 REST，但每个端点的 scope
// 仍由 restScopeMiddleware 控制。
func jwtOrPATAuthMiddleware(patMw, jwtMw gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		patMw(c)
		if c.IsAborted() {
			return
		}
		if APITokenFromContext(c) != nil {
			return
		}
		jwtMw(c)
		if c.IsAborted() {
			return
		}
	}
}

// patOrFallbackAuthMiddleware 是 optional 路由（ForceAuth=false 时也能匿名访问）
// 的鉴权链：
//  1. apiTokenAuthMiddleware：识别 PAT，命中后挂 user；非法 PAT 401 abort。
//  2. 已挂 PAT → 跳过 JWT，restScopeMiddleware 会按 scope 收口。
//  3. 未带 PAT → 走 fallbackJwtMw，存在 JWT 则挂 user，没有就匿名继续。
//
// 这是修复 ForceAuth=false 时 optional 路由完全不解析 PAT 的关键：
// 之前直接用 fallbackAuthMw 会让 PAT 请求被当作 guest，scope 形同虚设。
func patOrFallbackAuthMiddleware(patMw, fallbackJwtMw gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		patMw(c)
		if c.IsAborted() {
			return
		}
		if APITokenFromContext(c) != nil {
			return
		}
		fallbackJwtMw(c)
	}
}

// restScopeMiddleware 在 /api/v1/* 路由上 enforce PAT scope。
//
// 行为：
//   - JWT 持有者（任何来源：cookie / Authorization Bearer 非 nzp_）→ 直接放行，
//     沿用 JWT 模型的完整权限。
//   - PAT 持有者 → 必须命中给定 scope。命中后下游 handler 仍受 user 级权限
//     检查（adminHandler / Server.HasPermission），scope 只能收窄不能放大。
//   - PAT 持有者遇到 scope=="" → 直接 403。空字符串作为 fail-closed 默认值，
//     防止接入新路由时忘填 scope 把 PAT 静默放行。
//
// 因此"自我管理"端点（/profile、/api-tokens、/refresh-token 等）必须显式挂
// restPATForbiddenMiddleware 来拒绝 PAT，而不是依赖空 scope 兜底。
func restScopeMiddleware(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := APITokenFromContext(c)
		if tok == nil {
			c.Next()
			return
		}
		if scope == "" || !tok.HasScope(scope) {
			c.AbortWithStatusJSON(http.StatusForbidden, model.CommonResponse[any]{
				Success: false,
				Error:   "ApiErrorForbidden: api token lacks scope " + scope,
			})
			return
		}
		c.Next()
	}
}

// restScopeAllOf is the multi-scope variant of restScopeMiddleware. It
// gates on EVERY listed scope, used by routes whose semantics span more
// than one capability — file-manager sessions read, write AND delete
// files, so a PAT that only carries nezha:server:write must NOT be allowed
// to open one. JWT callers pass through unchanged.
func restScopeAllOf(scopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := APITokenFromContext(c)
		if tok == nil {
			c.Next()
			return
		}
		for _, scope := range scopes {
			if scope == "" || !tok.HasScope(scope) {
				c.AbortWithStatusJSON(http.StatusForbidden, model.CommonResponse[any]{
					Success: false,
					Error:   "ApiErrorForbidden: api token lacks scope " + scope,
				})
				return
			}
		}
		c.Next()
	}
}

// serverConfigSensitiveScope 收紧 GET /server/config/:id 的 PAT scope 到
// ScopeServerWrite：返回体里包含 client_secret 等下发到 agent 的凭据，单纯
// nezha:server:read 不应足以读取。命名刻意带 Sensitive 而不是 Read，避免下
// 个维护者把它当成普通 read scope 还原成 ScopeServerRead 重新打开提权链。
func serverConfigSensitiveScope() string { return model.ScopeServerWrite }

// restPATForbiddenMiddleware 在「自我管理」类端点上显式拒绝 PAT。
//
// 这些端点（profile / api-tokens / oauth2 绑定 / refresh-token）一旦允许 PAT
// 自调，就可形成提权链（PAT → 创建更高权限 PAT → ...）。
// 显式 403 比静默放行更安全。
func restPATForbiddenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if APITokenFromContext(c) != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, model.CommonResponse[any]{
				Success: false,
				Error:   "ApiErrorForbidden: this endpoint is not accessible by api token",
			})
			return
		}
		c.Next()
	}
}
