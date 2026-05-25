package model

import (
	"encoding/json"
	"testing"
)

// Server.MarshalJSON projects Server.UserID into a public owner field while
// keeping the raw UserID json-hidden. The lookup function is package-level
// and shared across tests; each subtest installs its own stub and restores
// the original to avoid leaking state.
func TestServerMarshalJSONOwnerProjection(t *testing.T) {
	original := ServerOwnerLookup
	t.Cleanup(func() { ServerOwnerLookup = original })

	tests := []struct {
		name        string
		uid         uint64
		lookup      func(uid uint64) (ServerOwnerInfo, bool)
		wantID      uint64
		wantHasName bool
		wantName    string
	}{
		{
			// uid=0 is the legacy global agent secret pseudo-owner. The
			// lookup deliberately returns ok=false so the frontend can
			// render it as "Global Agent" instead of a real username.
			name: "uid_zero_has_no_username",
			uid:  0,
			lookup: func(uint64) (ServerOwnerInfo, bool) {
				return ServerOwnerInfo{}, false
			},
			wantID:      0,
			wantHasName: false,
		},
		{
			// Known user → username flows through to the wire so the
			// admin frontend can show it without a separate /user fetch
			// (which members cannot call anyway).
			name: "known_user_has_username",
			uid:  42,
			lookup: func(uid uint64) (ServerOwnerInfo, bool) {
				return ServerOwnerInfo{ID: uid, Username: "alice"}, true
			},
			wantID:      42,
			wantHasName: true,
			wantName:    "alice",
		},
		{
			// Deleted user → lookup returns ok=false. The wire still
			// carries owner.id so the frontend can render an "Unknown
			// user (#id)" placeholder; otherwise the row would silently
			// appear ownerless and ops would lose the audit trail.
			name: "deleted_user_keeps_id_without_username",
			uid:  999,
			lookup: func(uint64) (ServerOwnerInfo, bool) {
				return ServerOwnerInfo{}, false
			},
			wantID:      999,
			wantHasName: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ServerOwnerLookup = tc.lookup
			s := &Server{Common: Common{ID: 7, UserID: tc.uid}, Name: "srv"}

			raw, err := json.Marshal(s)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var got struct {
				Owner *ServerOwnerInfo `json:"owner"`
				// Owner must never appear as the raw uid via Common.UserID;
				// the Common.UserID json tag is "-" and a regression that
				// flips it to "user_id" would expose internal owner ids
				// to the wire bypassing the lookup-controlled rendering.
				UserID *uint64 `json:"user_id,omitempty"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if got.UserID != nil {
				t.Fatalf("Common.UserID must not appear on the wire as user_id, got %d", *got.UserID)
			}
			if got.Owner == nil {
				t.Fatalf("owner field must always be present, raw=%s", raw)
			}
			if got.Owner.ID != tc.wantID {
				t.Fatalf("owner.id=%d, want %d", got.Owner.ID, tc.wantID)
			}
			if tc.wantHasName {
				if got.Owner.Username != tc.wantName {
					t.Fatalf("owner.username=%q, want %q", got.Owner.Username, tc.wantName)
				}
			} else if got.Owner.Username != "" {
				t.Fatalf("owner.username must be omitted for uid=%d, got %q", tc.uid, got.Owner.Username)
			}
		})
	}
}

// When no lookup is installed (tests / headless tools), MarshalJSON must
// still emit a minimal owner record so consumers do not crash on missing
// fields. Without this guard a future refactor could silently drop the
// owner key entirely whenever the hook is nil.
func TestServerMarshalJSONEmitsOwnerWithoutLookup(t *testing.T) {
	original := ServerOwnerLookup
	t.Cleanup(func() { ServerOwnerLookup = original })
	ServerOwnerLookup = nil

	raw, err := json.Marshal(&Server{Common: Common{ID: 1, UserID: 17}, Name: "srv"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got struct {
		Owner *ServerOwnerInfo `json:"owner"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Owner == nil || got.Owner.ID != 17 || got.Owner.Username != "" {
		t.Fatalf("expected bare owner record {id:17}, got %+v", got.Owner)
	}
}
