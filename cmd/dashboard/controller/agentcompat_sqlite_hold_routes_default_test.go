//go:build !agentcompat

package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAgentcompatSQLiteHoldRoutesAreAbsentWithoutBuildTag(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAgentcompatRoutes(router)
	request := httptest.NewRequest(http.MethodPost, "/agentcompat/sqlite-hold/arm", nil)
	response := httptest.NewRecorder()

	// When
	router.ServeHTTP(response, request)

	// Then
	require.Equal(t, http.StatusNotFound, response.Code)
}
