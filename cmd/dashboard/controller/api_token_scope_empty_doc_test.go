package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
)

// restScopeMiddleware 的"空 scope"实际行为：PAT 调用方一律 403。
// 这条测试把注释与实现的契约对齐：
//   1. 实际行为：PAT + scope="" → 403。
//   2. 文档约束：源码注释必须明确说出"空 scope 对 PAT 仍被拒绝"，
//      不能再保留"空字符串 = 放行"这种与实现相反的旧措辞。
func TestRestScopeMiddleware_EmptyScopeRejectsPAT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		c.Set(apiTokenCtxKey, &model.APIToken{ID: 1})
		c.Set(model.CtxKeyAPIToken, &model.APIToken{ID: 1})
		c.Next()
	}, restScopeMiddleware(""), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when PAT hits restScopeMiddleware(\"\"); got %d body=%q", w.Code, w.Body.String())
	}
}

func TestRestScopeMiddleware_DocReflectsEmptyScopeRejection(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(wd, "api_token_scope.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	src := string(b)
	idx := strings.Index(src, "func restScopeMiddleware(")
	if idx < 0 {
		t.Fatalf("restScopeMiddleware not found")
	}
	doc := src[:idx]
	if strings.Contains(doc, "空字符串）= 放行") || strings.Contains(doc, "空字符串) = 放行") {
		t.Fatalf("doc still claims empty scope means 放行; this contradicts the implementation which 403s PAT callers")
	}
}
