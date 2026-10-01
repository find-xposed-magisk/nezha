package controller

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestGetServerConfigForeignAndUnknownIDsAreIndistinguishable(t *testing.T) {
	cleanup, _ := setupMCPTest(t)
	defer cleanup()
	require.NoError(t, singleton.DB.Create(&model.User{Common: model.Common{ID: 200}, Username: "bob", Role: model.RoleMember}).Error)
	tok, _ := mkToken(t, 200, []string{model.ScopeServerRead}, nil)

	probe := func(id string) error {
		c, _ := patRequestCtx(t, tok, 200, http.MethodGet, "/api/v1/server/config/"+id, nil)
		c.Params = gin.Params{{Key: "id", Value: id}}
		_, err := getServerConfig(c)
		return err
	}

	foreignErr := probe("7")
	unknownErr := probe("999999")
	require.Error(t, foreignErr)
	require.Error(t, unknownErr)
	require.Equal(t, foreignErr.Error(), unknownErr.Error())
}

func TestSetServerConfigForeignAndUnknownIDsAreIndistinguishable(t *testing.T) {
	cleanup, _ := setupMCPTest(t)
	defer cleanup()
	require.NoError(t, singleton.DB.Create(&model.User{Common: model.Common{ID: 200}, Username: "bob", Role: model.RoleMember}).Error)
	tok, _ := mkToken(t, 200, []string{model.ScopeServerWrite}, nil)

	probe := func(id uint64) error {
		c, _ := patRequestCtx(t, tok, 200, http.MethodPost, "/api/v1/server/config", model.ServerConfigForm{
			Servers: []uint64{id},
			Config:  "{}",
		})
		_, err := setServerConfig(c)
		return err
	}

	foreignErr := probe(7)
	unknownErr := probe(999999)
	require.Error(t, foreignErr)
	require.Error(t, unknownErr)
	require.Equal(t, foreignErr.Error(), unknownErr.Error())
}
