package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBatchDeleteNotificationCapsRequestBody(t *testing.T) {
	body := append(bytes.Repeat([]byte{' '}, notificationBatchDeleteMaxBodyBytes+1), '[', ']')
	req := httptest.NewRequest(http.MethodPost, "/api/v1/batch-delete/notification", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	_, err := batchDeleteNotification(c)
	if err == nil || !strings.Contains(err.Error(), "request body too large") {
		t.Fatalf("oversized request body was not rejected by MaxBytesReader: %v", err)
	}
}
