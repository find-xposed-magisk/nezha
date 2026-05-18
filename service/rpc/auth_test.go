package rpc

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// setupAuthAgentFixture seeds an in-memory DB and ServerShared with two
// servers belonging to different users so we can assert that a secret bound
// to user A cannot resolve a server UUID owned by user B.
func setupAuthAgentFixture(t *testing.T) func() {
	t.Helper()
	originalDB := singleton.DB
	originalServerShared := singleton.ServerShared

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Server{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&model.Server{
		Common: model.Common{ID: 1, UserID: 100},
		UUID:   "uuid-alice",
		Name:   "alice-srv",
	}).Error; err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := db.Create(&model.Server{
		Common: model.Common{ID: 2, UserID: 200},
		UUID:   "uuid-bob",
		Name:   "bob-srv",
	}).Error; err != nil {
		t.Fatalf("create bob: %v", err)
	}
	singleton.DB = db
	singleton.ServerShared = singleton.NewServerClass()

	return func() {
		singleton.DB = originalDB
		singleton.ServerShared = originalServerShared
	}
}

func TestAuthorizeAgentForUUIDAcceptsOwnedServer(t *testing.T) {
	defer setupAuthAgentFixture(t)()

	cid, hasID, err := authorizeAgentForUUID(100, "uuid-alice")
	if err != nil {
		t.Fatalf("alice with her own server UUID must not error, got %v", err)
	}
	if !hasID || cid != 1 {
		t.Fatalf("expected (cid=1, hasID=true), got (cid=%d, hasID=%v)", cid, hasID)
	}
}

// Core regression: an agent presenting user A's secret but user B's server
// UUID must be rejected. Previously the code returned the resolved server ID
// without verifying the UserID matched the secret owner, allowing same-tenant
// (and worse — cross-tenant if UUID leaks) server impersonation.
func TestAuthorizeAgentForUUIDRejectsForeignServerUUID(t *testing.T) {
	defer setupAuthAgentFixture(t)()

	_, _, err := authorizeAgentForUUID(100, "uuid-bob") // alice's secret + bob's UUID
	if err == nil {
		t.Fatalf("UUID owned by another user must be rejected")
	}
}

// An unknown UUID must NOT be treated as an impersonation attempt — it is
// the normal first-time registration path and the caller (Check) creates a
// new server bound to the secret owner.
func TestAuthorizeAgentForUUIDPermitsUnknownUUIDForRegistration(t *testing.T) {
	defer setupAuthAgentFixture(t)()

	cid, hasID, err := authorizeAgentForUUID(100, "uuid-never-seen-before")
	if err != nil {
		t.Fatalf("unknown UUID must be permitted for new registration, got %v", err)
	}
	if hasID {
		t.Fatalf("hasID must be false for unknown UUID, got cid=%d", cid)
	}
}
