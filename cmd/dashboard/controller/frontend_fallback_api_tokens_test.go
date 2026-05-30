package controller

import (
	"net/http"
	"strings"
	"testing"
)

// 前端在 main.tsx 注册了 /dashboard/settings/api-tokens，但后端 fallback 白名单
// 漏加这条会让用户直接刷新该页面拿到 HTTP 404（body 还是 index.html）。
// controller.go 旁边的注释明确说「新增前端路由时必须在 main.tsx 与这里同步加」。
func TestFallbackToFrontend_APITokensRouteReturns200(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newFrontendFallbackTestRouter(t)

	w := performFrontendFallbackRequest(t, router, "/dashboard/settings/api-tokens")
	if w.Code != http.StatusOK {
		t.Fatalf("/dashboard/settings/api-tokens fallback status = %d, want 200 "+
			"(front-end main.tsx registered the route — backend SPA fallback regex must mirror it)",
			w.Code)
	}
	if !strings.Contains(w.Body.String(), "admin index") {
		t.Fatalf("/dashboard/settings/api-tokens must serve admin index.html, got %q", w.Body.String())
	}
}
