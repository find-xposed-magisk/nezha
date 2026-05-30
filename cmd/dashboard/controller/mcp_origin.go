package controller

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func mcpOriginGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" {
			c.Next()
			return
		}
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			abortOrigin(c)
			return
		}
		if !strings.EqualFold(u.Host, c.Request.Host) {
			abortOrigin(c)
			return
		}
		// DNS rebinding 防线：当 dashboard 明确仅暴露在 loopback 上时，浏览器只
		// 应通过 loopback hostname 命中本服务。任何带 Origin 的请求若 Host 不是
		// loopback 字面量，说明它解析自一个对外 DNS 名（典型攻击：evil.example
		// 解析到 127.0.0.1，Host == Origin == evil.example 等式成立但实际打的是
		// 用户本机 dashboard）。绑公网 IP / 未指定地址 / 公网 hostname 的部署
		// 跳过此检查 —— 那种部署下 Host 本来就是公网域名，再要求 loopback
		// 会把合法的同源前端请求一刀切。
		if dashboardListensOnLoopback() && !isLoopbackHostname(hostnameOnly(c.Request.Host)) {
			abortOrigin(c)
			return
		}
		c.Next()
	}
}

// dashboardListensOnLoopback 判定 dashboard 是否仅暴露在 loopback 上。
//
// 之前把未指定地址（空字符串、`0.0.0.0`、`::`、`*`）也当 loopback 部署，理由是
// 这些场景下浏览器仍可能通过 127.0.0.1 访问，rebinding 风险存在。问题是绝大多数
// 生产部署就是 `listen_host=""` / `0.0.0.0`，配合公网域名访问；那种部署下浏览器
// 同源 POST 的 Host 是公网域名而不是 loopback，旧逻辑会把合法的同源前端请求一刀切。
//
// 现在只在 ListenHost 明确写成 loopback 字面量时才开启 loopback 严格化：
//   - 显式 loopback IP（127.0.0.1 / ::1）或 localhost → 视为 loopback 部署，
//     此时任何带 Origin 的请求 Host 不是 loopback 都拒掉（DNS rebinding 防线）；
//   - 空 / `0.0.0.0` / `::` / `*` / 公网 IP / hostname → 不当 loopback 部署，
//     仅做 Origin == Host 的同源校验，不再额外要求 Host 必须是 loopback。
//
// 调用方仍然在配额（Origin == Host）外用 isLoopbackHostname 校验 Host，所以
// 当本机用户拿 127.0.0.1 访问绑公网 IP 的 dashboard 时这条防线依然生效（Host
// 是 loopback，Origin 是公网域名，hostnameOnly 比较会失败）。
func dashboardListensOnLoopback() bool {
	if singleton.Conf == nil {
		return false
	}
	host := strings.TrimSpace(singleton.Conf.ListenHost)
	if host == "" {
		return false
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "0.0.0.0", "::", "*":
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

func hostnameOnly(hostport string) string {
	if i := strings.LastIndex(hostport, ":"); i >= 0 {
		if !strings.Contains(hostport[i:], "]") {
			return hostport[:i]
		}
	}
	return hostport
}

func isLoopbackHostname(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func abortOrigin(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, model.CommonResponse[any]{
		Success: false,
		Error:   "ApiErrorForbidden: origin not allowed",
	})
}

func mcpMethodNotAllowed(c *gin.Context) {
	c.Header("Allow", "POST")
	c.JSON(http.StatusMethodNotAllowed, model.CommonResponse[any]{
		Success: false,
		Error:   "MCP endpoint only accepts POST (Streamable HTTP without standalone SSE / sessions)",
	})
}
