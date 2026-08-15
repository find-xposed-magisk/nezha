package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func serviceTypeSecurityRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, 100, model.RoleMember)
		c.Next()
	})
	r.POST("/api/v1/service", commonHandler(createService))
	r.PATCH("/api/v1/service/:id", commonHandler(updateService))
	return r
}

func serviceTypeSecurityBody(taskType uint8) []byte {
	body, _ := json.Marshal(model.ServiceForm{
		Name:        "service-type-security",
		Target:      "example.invalid:443",
		Type:        taskType,
		Cover:       model.ServiceCoverIgnoreAll,
		SkipServers: map[uint64]bool{1: true},
		Duration:    30,
	})
	return body
}

func TestCreateServiceRejectsNonProbeTaskTypes(t *testing.T) {
	setupCoverPATFixture(t)
	r := serviceTypeSecurityRouter()

	for _, taskType := range []uint8{0, model.TaskTypeCommand, model.TaskTypeApplyConfig, model.TaskTypeExec, 255} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/service", bytes.NewReader(serviceTypeSecurityBody(taskType)))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
		require.False(t, success, "type %d must be rejected", taskType)
		require.Contains(t, errMsg, "invalid service monitor type")
	}

	var count int64
	require.NoError(t, singleton.DB.Model(&model.Service{}).Count(&count).Error)
	require.Zero(t, count, "rejected task types must not reach persistence")
}

func TestUpdateServiceRejectsNonProbeTaskTypes(t *testing.T) {
	setupCoverPATFixture(t)
	r := serviceTypeSecurityRouter()
	service := &model.Service{
		Common:      model.Common{UserID: 100},
		Name:        "valid-service",
		Target:      "example.invalid:443",
		Type:        model.TaskTypeTCPPing,
		Cover:       model.ServiceCoverIgnoreAll,
		SkipServers: map[uint64]bool{1: true},
		Duration:    30,
	}
	require.NoError(t, singleton.DB.Create(service).Error)

	for _, taskType := range []uint8{model.TaskTypeCommand, model.TaskTypeApplyConfig, model.TaskTypeExec, 255} {
		w := httptest.NewRecorder()
		path := fmt.Sprintf("/api/v1/service/%d", service.ID)
		req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(serviceTypeSecurityBody(taskType)))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
		require.False(t, success, "type %d must be rejected", taskType)
		require.Contains(t, errMsg, "invalid service monitor type")

		var persisted model.Service
		require.NoError(t, singleton.DB.First(&persisted, service.ID).Error)
		require.Equal(t, uint8(model.TaskTypeTCPPing), persisted.Type)
	}
}
