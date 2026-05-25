package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	rpc.NezhaHandlerSingleton.CreateStream("alice-terminal", 100, 1)

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
	rpc.NezhaHandlerSingleton.CreateStream("alice-fm", 100, 1)

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

// JWT cookie security: SigningAlgorithm must be pinned to HS256 (defense
// against future algorithm-confusion regressions in the library) and the
// JWT cookie must use SameSite=Lax so cross-site GET navigations don't
// silently mint requests with the user's session. HttpOnly/Secure are NOT
// asserted here because the frontend currently reads `!!document.cookie`
// to display login state and many deployments terminate TLS at a proxy —
// flipping those would break user-visible behaviour and is tracked
// separately.
func TestJWTInitParamsPinsAlgorithmAndSameSite(t *testing.T) {
	ensureLocalizerForStreamTests(t)
	if singleton.Conf == nil {
		singleton.Conf = &singleton.ConfigClass{
			Config: &model.Config{JWTSecretKey: "test-secret-for-jwt-config-assertions"},
		}
	}
	params := initParams()
	if params.SigningAlgorithm != "HS256" {
		t.Fatalf("SigningAlgorithm must be pinned to HS256, got %q", params.SigningAlgorithm)
	}
	if params.CookieSameSite != http.SameSiteLaxMode {
		t.Fatalf("CookieSameSite must be Lax for OAuth-callback compatibility + CSRF safety, got %v", params.CookieSameSite)
	}
}

// MEDIUM security: an IOStream session created by the old owner (terminal,
// file-manager, NAT) must be torn down when the server's ownership rotates
// — Register on Initiate, revertTransition on Cancel/Fail/Timeout, and
// OnServersDeleted on delete. Otherwise the old owner keeps an open
// websocket attached to a server they no longer own, which is effectively
// post-transfer RCE / file-read.
func TestServerTransferTransitionRevokesActiveIOStreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ensureLocalizerForStreamTests(t)
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	originalHook := singleton.ServerTransferStreamRevocationHook
	singleton.ServerTransferStreamRevocationHook = rpc.NezhaHandlerSingleton.RevokeStreamsForServer
	defer func() {
		singleton.ServerTransferStreamRevocationHook = originalHook
	}()

	rpc.NezhaHandlerSingleton.CreateStream("term-server-1", 100, 1)
	rpc.NezhaHandlerSingleton.CreateStream("fm-server-1", 100, 1)
	rpc.NezhaHandlerSingleton.CreateStream("term-server-2", 100, 2)

	singleton.ServerTransferRevokeStreamsForServer(1)

	if _, exists := rpc.NezhaHandlerSingleton.StreamOwnership("term-server-1"); exists {
		t.Fatal("terminal stream for transferred server 1 must be revoked on ownership rotation")
	}
	if _, exists := rpc.NezhaHandlerSingleton.StreamOwnership("fm-server-1"); exists {
		t.Fatal("file-manager stream for transferred server 1 must be revoked on ownership rotation")
	}
	if _, exists := rpc.NezhaHandlerSingleton.StreamOwnership("term-server-2"); !exists {
		t.Fatal("unrelated server's stream must NOT be revoked")
	}
}

// nz-o2s carries the OAuth2 state binding that authenticates the callback.
// The frontend never reads it, so HttpOnly is safe to enable and shuts the
// door on XSS attempting to steal the state.
func TestWriteOauth2StateCookieIsHttpOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	writeOauth2StateCookie(c, "test-key")

	header := w.Header().Get("Set-Cookie")
	if !strings.Contains(header, "nz-o2s=test-key") {
		t.Fatalf("expected nz-o2s cookie in response, got %q", header)
	}
	if !strings.Contains(header, "HttpOnly") {
		t.Fatalf("nz-o2s must be HttpOnly to prevent XSS reading OAuth state, got %q", header)
	}
}
