//go:build !agentcompat

package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDefaultBuildDoesNotRegisterAgentcompatCapabilityRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAgentcompatRoutes(router)
	request := httptest.NewRequest(http.MethodPost, "/agentcompat/io-stream-capability/register", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusNotFound, response.Code)
}
