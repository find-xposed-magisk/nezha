package controller

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestListDDNS_RedactsCredentials(t *testing.T) {
	defer setupTenancyTest(t)()

	p := model.DDNSProfile{
		Common:         model.Common{UserID: 10},
		Name:           "cf",
		Provider:       "cloudflare",
		AccessID:       "id",
		AccessSecret:   "super-secret-token",
		WebhookHeaders: `{"Authorization":"Bearer xxx"}`,
	}
	require.NoError(t, singleton.DB.Create(&p).Error)
	singleton.DDNSShared.InsertForTest(&p)

	c := ctxAs(10, model.RoleAdmin)
	out, err := listDDNS(c)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Empty(t, out[0].AccessSecret, "access_secret must be redacted in list response")
	require.Empty(t, out[0].WebhookHeaders, "webhook_headers must be redacted in list response")
	require.Equal(t, "id", out[0].AccessID, "non-secret fields must be preserved")

	var stored model.DDNSProfile
	require.NoError(t, singleton.DB.First(&stored, p.ID).Error)
	require.Equal(t, "super-secret-token", stored.AccessSecret, "redaction must not mutate stored data")
}

func TestListNotification_RedactsCredentials(t *testing.T) {
	defer setupTenancyTest(t)()

	n := model.Notification{
		Common:        model.Common{UserID: 10},
		Name:          "slack",
		URL:           "https://hooks.slack.com/services/T/B/secret",
		RequestHeader: `{"Authorization":"Bearer xxx"}`,
		RequestBody:   `{"text":"#NEZHA#"}`,
	}
	require.NoError(t, singleton.DB.Create(&n).Error)
	singleton.NotificationShared.InsertForTest(&n)

	c := ctxAs(10, model.RoleAdmin)
	out, err := listNotification(c)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Empty(t, out[0].URL, "url must be redacted in list response")
	require.Empty(t, out[0].RequestHeader, "request_header must be redacted in list response")
	require.Empty(t, out[0].RequestBody, "request_body must be redacted in list response")
	require.Equal(t, "slack", out[0].Name, "non-secret fields must be preserved")

	var stored model.Notification
	require.NoError(t, singleton.DB.First(&stored, n.ID).Error)
	require.Equal(t, "https://hooks.slack.com/services/T/B/secret", stored.URL,
		"redaction must not mutate stored data")
}

func TestUpdateDDNS_EmptySecretPreservesStored(t *testing.T) {
	defer setupTenancyTest(t)()

	existing := model.DDNSProfile{
		Common:         model.Common{UserID: 10},
		Name:           "cf",
		Provider:       "webhook",
		AccessID:       "id",
		AccessSecret:   "keep-me",
		WebhookURL:     "http://127.0.0.1/",
		WebhookMethod:  1,
		WebhookHeaders: `{"X-Token":"keep-header"}`,
	}
	require.NoError(t, singleton.DB.Create(&existing).Error)
	singleton.DDNSShared.InsertForTest(&existing)

	c := ctxAsMemberWithBody(10, map[string]any{
		"name":                 "cf-renamed",
		"provider":             "webhook",
		"access_id":            "id",
		"access_secret":        "",
		"webhook_url":          "http://127.0.0.1/",
		"webhook_method":       1,
		"webhook_request_type": 1,
		"webhook_headers":      "",
		"max_retries":          3,
	})
	c.Params = gin.Params{{Key: "id", Value: itoa(existing.ID)}}
	_, err := updateDDNS(c)
	require.NoError(t, err)

	var after model.DDNSProfile
	require.NoError(t, singleton.DB.First(&after, existing.ID).Error)
	require.Equal(t, "cf-renamed", after.Name, "non-secret edits must apply")
	require.Equal(t, "keep-me", after.AccessSecret, "empty submitted secret must preserve stored value")
	require.Equal(t, `{"X-Token":"keep-header"}`, after.WebhookHeaders,
		"empty submitted headers must preserve stored value")
}

func TestUpdateDDNS_NonEmptySecretOverwrites(t *testing.T) {
	defer setupTenancyTest(t)()

	existing := model.DDNSProfile{
		Common:        model.Common{UserID: 10},
		Name:          "cf",
		Provider:      "webhook",
		AccessSecret:  "old",
		WebhookURL:    "http://127.0.0.1/",
		WebhookMethod: 1,
	}
	require.NoError(t, singleton.DB.Create(&existing).Error)
	singleton.DDNSShared.InsertForTest(&existing)

	c := ctxAsMemberWithBody(10, map[string]any{
		"name":                 "cf",
		"provider":             "webhook",
		"access_secret":        "new-secret",
		"webhook_url":          "http://127.0.0.1/",
		"webhook_method":       1,
		"webhook_request_type": 1,
		"max_retries":          3,
	})
	c.Params = gin.Params{{Key: "id", Value: itoa(existing.ID)}}
	_, err := updateDDNS(c)
	require.NoError(t, err)

	var after model.DDNSProfile
	require.NoError(t, singleton.DB.First(&after, existing.ID).Error)
	require.Equal(t, "new-secret", after.AccessSecret, "non-empty submitted secret must overwrite")
}

func TestUpdateNotification_EmptyFieldsPreserveStored(t *testing.T) {
	defer setupTenancyTest(t)()

	existing := model.Notification{
		Common:        model.Common{UserID: 10},
		Name:          "slack",
		URL:           "https://hooks.slack.com/services/keep",
		RequestMethod: model.NotificationRequestMethodGET,
		RequestType:   model.NotificationRequestTypeJSON,
		RequestHeader: `{"Authorization":"keep"}`,
		RequestBody:   "",
	}
	require.NoError(t, singleton.DB.Create(&existing).Error)
	singleton.NotificationShared.InsertForTest(&existing)

	c := ctxAsMemberWithBody(10, map[string]any{
		"name":           "slack-renamed",
		"url":            "",
		"request_method": model.NotificationRequestMethodGET,
		"request_type":   model.NotificationRequestTypeJSON,
		"request_header": "",
		"request_body":   "",
		"skip_check":     true,
	})
	c.Params = gin.Params{{Key: "id", Value: itoa(existing.ID)}}
	_, err := updateNotification(c)
	require.NoError(t, err)

	var after model.Notification
	require.NoError(t, singleton.DB.First(&after, existing.ID).Error)
	require.Equal(t, "slack-renamed", after.Name, "non-secret edits must apply")
	require.Equal(t, "https://hooks.slack.com/services/keep", after.URL,
		"empty submitted url must preserve stored value")
	require.Equal(t, `{"Authorization":"keep"}`, after.RequestHeader,
		"empty submitted request_header must preserve stored value")
}
