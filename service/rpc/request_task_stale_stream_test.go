package rpc

import (
	"context"
	"errors"
	"testing"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// When a server is edited mid-session, updateServer swaps a new *Server into
// ServerShared that adopts the live stream holder. The agent's RequestTask
// cleanup must detach the stream from whichever *Server is currently published,
// not the stale object captured when the stream attached — otherwise the new
// object keeps reporting the agent as online on a dead stream.
func TestRequestTaskCleanupDetachesStreamFromCurrentServerAfterEdit(t *testing.T) {
	reporter := requestTaskSecurityServer(7, 200, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	setupRequestTaskSecurityFixture(t, []*model.Server{reporter}, nil, map[uint64]model.UserInfo{
		200: {Role: model.RoleMember},
	}, map[string]uint64{"reporter-secret": 200})

	old, ok := singleton.ServerShared.Get(reporter.ID)
	if !ok {
		t.Fatalf("server %d not found", reporter.ID)
	}

	stream := requestTaskSecurityAuthedStream("reporter-secret", reporter.UUID)
	stream.onRecv = func() {
		edited := &model.Server{Common: model.Common{ID: old.ID, UserID: old.UserID}, UUID: old.UUID, Name: "edited"}
		edited.CopyFromRunningServer(old)
		singleton.ServerShared.Update(edited, "")
	}

	if err := NewNezhaHandler().RequestTask(stream); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected RequestTask to finish after Recv error, got %v", err)
	}

	current, ok := singleton.ServerShared.Get(reporter.ID)
	if !ok {
		t.Fatalf("server %d not found after edit", reporter.ID)
	}
	if got := current.GetTaskStream(); got != nil {
		t.Fatalf("edited server must report offline after the agent stream dropped, got %T", got)
	}
}
