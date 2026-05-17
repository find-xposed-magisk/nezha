package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/nezhahq/nezha/model"
)

func makeStreamTestServers() []*model.Server {
	return []*model.Server{
		{
			Common:       model.Common{ID: 1, UserID: 100},
			Name:         "alice-public",
			PublicNote:   "alice-public-note",
			DisplayIndex: 0,
			HideForGuest: false,
			Host: &model.Host{
				Platform: "linux", PlatformVersion: "6.1",
				CPU: []string{"amd64"}, Version: "agent-v1", GPU: []string{"rtx"},
			},
			State:      &model.HostState{CPU: 0.1},
			LastActive: time.Unix(1_700_000_000, 0).UTC(),
		},
		{
			Common:       model.Common{ID: 2, UserID: 100},
			Name:         "alice-hidden",
			PublicNote:   "alice-hidden-note",
			DisplayIndex: 0,
			HideForGuest: true,
			Host: &model.Host{
				Platform: "linux", PlatformVersion: "6.5",
				CPU: []string{"amd64"}, Version: "agent-v2", GPU: []string{"rtx"},
			},
			State:      &model.HostState{CPU: 0.2},
			LastActive: time.Unix(1_700_000_001, 0).UTC(),
		},
		{
			Common:       model.Common{ID: 3, UserID: 200},
			Name:         "bob-public",
			PublicNote:   "bob-public-note",
			DisplayIndex: 0,
			HideForGuest: false,
			Host: &model.Host{
				Platform: "darwin", PlatformVersion: "14.0",
				CPU: []string{"arm64"}, Version: "agent-v3", GPU: []string{"m2"},
			},
			State:      &model.HostState{CPU: 0.3},
			LastActive: time.Unix(1_700_000_002, 0).UTC(),
		},
		{
			Common:       model.Common{ID: 4, UserID: 200},
			Name:         "bob-hidden",
			PublicNote:   "bob-hidden-note",
			DisplayIndex: 0,
			HideForGuest: true,
			Host: &model.Host{
				Platform: "darwin", PlatformVersion: "14.1",
				CPU: []string{"arm64"}, Version: "agent-v4", GPU: []string{"m2"},
			},
			State:      &model.HostState{CPU: 0.4},
			LastActive: time.Unix(1_700_000_003, 0).UTC(),
		},
	}
}

func findStreamServer(out []model.StreamServer, id uint64) *model.StreamServer {
	for i := range out {
		if out[i].ID == id {
			return &out[i]
		}
	}
	return nil
}

// Guest: no auth → skip every HideForGuest server, Host.Filter() drops
// PlatformVersion and agent Version while keeping the rest (including GPU).
func TestFilterServersForViewerGuestHidesPrivateAndRedactsHost(t *testing.T) {
	out := filterServersForViewer(makeStreamTestServers(), 0, false, true)

	assert.Len(t, out, 2)
	assert.Nil(t, findStreamServer(out, 2), "alice-hidden should be invisible to guests")
	assert.Nil(t, findStreamServer(out, 4), "bob-hidden should be invisible to guests")

	alicePublic := findStreamServer(out, 1)
	if assert.NotNil(t, alicePublic) {
		assert.Empty(t, alicePublic.Host.PlatformVersion, "guest must not see PlatformVersion")
		assert.Empty(t, alicePublic.Host.Version, "guest must not see agent Version")
		assert.Equal(t, "linux", alicePublic.Host.Platform, "non-sensitive Platform stays visible")
		assert.Equal(t, "alice-public-note", alicePublic.PublicNote)
	}
}

// Non-owner member must see exactly the same data as a guest:
// no HideForGuest servers, and Host details on visible servers are redacted.
func TestFilterServersForViewerNonOwnerMemberMatchesGuest(t *testing.T) {
	servers := makeStreamTestServers()
	carolID := uint64(300)
	out := filterServersForViewer(servers, carolID, false, true)

	assert.Len(t, out, 2)
	assert.Nil(t, findStreamServer(out, 2))
	assert.Nil(t, findStreamServer(out, 4))

	bobPublic := findStreamServer(out, 3)
	if assert.NotNil(t, bobPublic) {
		assert.Empty(t, bobPublic.Host.PlatformVersion)
		assert.Empty(t, bobPublic.Host.Version)
	}
}

// Owner member: sees own HideForGuest servers with full Host, sees others'
// visible servers with redacted Host, never sees others' hidden servers.
func TestFilterServersForViewerOwnerSeesOwnHiddenAndFullHost(t *testing.T) {
	servers := makeStreamTestServers()
	aliceID := uint64(100)
	out := filterServersForViewer(servers, aliceID, false, true)

	assert.Len(t, out, 3, "alice sees her 2 servers + bob's 1 public server")
	assert.Nil(t, findStreamServer(out, 4), "alice must not see bob's hidden server")

	aliceHidden := findStreamServer(out, 2)
	if assert.NotNil(t, aliceHidden) {
		assert.Equal(t, "6.5", aliceHidden.Host.PlatformVersion, "owner sees full Host on her own hidden server")
		assert.Equal(t, "agent-v2", aliceHidden.Host.Version)
	}

	bobPublic := findStreamServer(out, 3)
	if assert.NotNil(t, bobPublic) {
		assert.Empty(t, bobPublic.Host.PlatformVersion, "non-owner Host is still redacted even for member viewer")
		assert.Empty(t, bobPublic.Host.Version)
	}
}

// Admin: no restrictions — sees every server with full Host, regardless of owner or HideForGuest.
func TestFilterServersForViewerAdminSeesAllWithFullHost(t *testing.T) {
	servers := makeStreamTestServers()
	out := filterServersForViewer(servers, 999, true, true)

	assert.Len(t, out, 4)
	for _, s := range out {
		assert.NotEmpty(t, s.Host.PlatformVersion, "admin must see PlatformVersion on every server")
		assert.NotEmpty(t, s.Host.Version, "admin must see agent Version on every server")
	}
}

// First-tick frame includes PublicNote, subsequent frames omit it.
// This must hold regardless of viewer.
func TestFilterServersForViewerWithoutPublicNoteFlagOmitsNote(t *testing.T) {
	out := filterServersForViewer(makeStreamTestServers(), 0, false, false)

	for _, s := range out {
		assert.Empty(t, s.PublicNote, "follow-up frames must not include PublicNote")
	}
}
