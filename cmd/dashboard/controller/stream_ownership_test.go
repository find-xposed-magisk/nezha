package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/i18n"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

func ensureLocalizerForStreamTests(t *testing.T) {
	t.Helper()
	if singleton.Localizer == nil {
		singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	}
	// upgrader stays nil — these tests must reject the caller BEFORE WS upgrade.
	// If a test ever reaches the upgrade path it will panic on nil upgrader,
	// surfacing the regression.
}

// decodeCommonResponseError returns Success and Error of a CommonResponse[any].
func decodeCommonResponseError(t *testing.T, body []byte) (bool, string) {
	t.Helper()
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, string(body))
	}
	return resp.Success, resp.Error
}

func setAuthUser(c *gin.Context, userID uint64, role model.Role) {
	c.Set(model.CtxKeyAuthorizedUser, &model.User{
		Common: model.Common{ID: userID},
		Role:   role,
	})
}

func TestTerminalStreamRejectsForeignMember(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ensureLocalizerForStreamTests(t)
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	rpc.NezhaHandlerSingleton.CreateStream("alice-terminal", 100)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, 200, model.RoleMember) // bob
		c.Next()
	})
	r.GET("/ws/terminal/:id", commonHandler(terminalStream))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws/terminal/alice-terminal", nil)
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.False(t, success, "foreign member must not be authorized to attach to alice's terminal")
	assert.Contains(t, errMsg, "permission denied")

	// And the existing stream must NOT have been torn down by the failed attempt.
	_, stillExists := rpc.NezhaHandlerSingleton.StreamOwnership("alice-terminal")
	assert.True(t, stillExists, "rejected attempt must not destroy the legitimate session")
}

func TestFMStreamRejectsForeignMember(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ensureLocalizerForStreamTests(t)
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	rpc.NezhaHandlerSingleton.CreateStream("alice-fm", 100)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, 200, model.RoleMember)
		c.Next()
	})
	r.GET("/ws/file/:id", commonHandler(fmStream))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws/file/alice-fm", nil)
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.False(t, success, "foreign member must not be authorized to attach to alice's FM session")
	assert.Contains(t, errMsg, "permission denied")

	_, stillExists := rpc.NezhaHandlerSingleton.StreamOwnership("alice-fm")
	assert.True(t, stillExists, "rejected attempt must not destroy the legitimate FM session")
}

func TestTerminalStreamRejectsUnknownStreamID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ensureLocalizerForStreamTests(t)
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, 100, model.RoleMember)
		c.Next()
	})
	r.GET("/ws/terminal/:id", commonHandler(terminalStream))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws/terminal/nonexistent", nil)
	r.ServeHTTP(w, req)

	success, _ := decodeCommonResponseError(t, w.Body.Bytes())
	assert.False(t, success, "unknown stream id must produce an error response")
}
