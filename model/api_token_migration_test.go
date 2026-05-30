package model

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&APIToken{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestNormalizeIncomingScope_RewritesReadOnlyMCPVariants(t *testing.T) {
	cases := map[string]string{
		"mcp:fs:read":     ScopeServerRead,
		"mcp:server:read": ScopeServerRead,
		"mcp:server:exec": ScopeServerExec,
	}
	for in, want := range cases {
		got, ok := NormalizeIncomingScope(in)
		if !ok {
			t.Fatalf("NormalizeIncomingScope(%q) ok=false; legacy read/exec must remain creatable", in)
		}
		if got != want {
			t.Fatalf("NormalizeIncomingScope(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeIncomingScope_RejectsDangerousLegacyVariants(t *testing.T) {
	for _, in := range []string{"mcp:fs:write", "mcp:fs:delete", "mcp:*", "mcp:unknown"} {
		if got, ok := NormalizeIncomingScope(in); ok {
			t.Errorf("NormalizeIncomingScope(%q) = (%q, true); legacy write/delete/wildcard must be rejected", in, got)
		}
	}
}

func TestNormalizeIncomingScope_PassesThroughNezhaScopes(t *testing.T) {
	got, ok := NormalizeIncomingScope(ScopeServerWrite)
	if !ok || got != ScopeServerWrite {
		t.Fatalf("nezha:* must pass through unchanged: got (%q, %v)", got, ok)
	}
}

func TestMigrateLegacyMCPScopes_RewritesReadOnlyAndDropsDangerous(t *testing.T) {
	db := newMigrationTestDB(t)

	tokens := []APIToken{
		{UserID: 1, Name: "read-only", TokenHash: HashAPIToken("nzp_a"), ScopesCSV: "mcp:fs:read"},
		{UserID: 2, Name: "mixed", TokenHash: HashAPIToken("nzp_b"), ScopesCSV: "mcp:server:read,mcp:fs:write"},
		{UserID: 3, Name: "purely-dangerous", TokenHash: HashAPIToken("nzp_c"), ScopesCSV: "mcp:fs:write,mcp:*"},
		{UserID: 4, Name: "modern", TokenHash: HashAPIToken("nzp_d"), ScopesCSV: ScopeServerRead},
	}
	for i := range tokens {
		if err := db.Create(&tokens[i]).Error; err != nil {
			t.Fatalf("seed token %d: %v", i, err)
		}
	}

	rewritten, deleted, err := MigrateLegacyMCPScopes(db)
	if err != nil {
		t.Fatalf("MigrateLegacyMCPScopes: %v", err)
	}
	if rewritten < 2 {
		t.Fatalf("expected >=2 rewrites (read-only + mixed); got %d", rewritten)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted token (purely-dangerous); got %d", deleted)
	}

	var got []APIToken
	if err := db.Order("id ASC").Find(&got).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 surviving tokens; got %d", len(got))
	}
	for _, tok := range got {
		if strings.Contains(tok.ScopesCSV, "mcp:") {
			t.Fatalf("token %d still carries legacy scope after migration: %q", tok.ID, tok.ScopesCSV)
		}
	}

	for _, tok := range got {
		switch tok.UserID {
		case 1:
			if tok.ScopesCSV != ScopeServerRead {
				t.Fatalf("uid=1 expected %q, got %q", ScopeServerRead, tok.ScopesCSV)
			}
		case 2:
			if tok.ScopesCSV != ScopeServerRead {
				t.Fatalf("uid=2 should keep only the safe read scope after drop; got %q", tok.ScopesCSV)
			}
		case 4:
			if tok.ScopesCSV != ScopeServerRead {
				t.Fatalf("uid=4 already-modern token must be untouched; got %q", tok.ScopesCSV)
			}
		default:
			t.Fatalf("unexpected surviving token uid=%d", tok.UserID)
		}
	}
}
