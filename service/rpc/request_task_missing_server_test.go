package rpc

import (
	"testing"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestAttachRequestTaskStream_MissingServerDoesNotPanic(t *testing.T) {
	reporter := requestTaskSecurityServer(7, 200, "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
	setupRequestTaskSecurityFixture(t, []*model.Server{reporter}, nil, map[uint64]model.UserInfo{
		200: {Role: model.RoleMember},
	}, map[string]uint64{"reporter-secret": 200})

	singleton.ServerShared.Delete([]uint64{reporter.ID})

	srv, ok := attachRequestTaskStream(reporter.ID, nil)
	if ok {
		t.Fatal("attach must report not-ok when the server was deleted between auth and lookup")
	}
	if srv != nil {
		t.Fatalf("attach must return a nil server for a deleted id, got %#v", srv)
	}
}
