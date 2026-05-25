package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/i18n"
	"github.com/nezhahq/nezha/service/singleton"
)

// listServerTransfer originally did `SELECT * FROM server_transfers ORDER BY
// id DESC` and relied on the listHandler post-filter (HasPermission) to drop
// rows the caller can't see. Functionally correct, but ServerTransfer is the
// only listX endpoint that hits the DB (the others serve in-memory caches),
// and it's an append-only audit table — every page load by a member who has
// participated in two transfers triggers a full table scan over the entire
// historical population. That cost is silent until the table is big and the
// dashboard slows for everyone at once. The fix pushes the same predicate
// HasPermission encodes down into the WHERE clause for non-admin callers.
//
// This test pins down the behavioural contract: regardless of the optimisation,
// a member must see only their own rows. It is intentionally written against
// the same response shape as the production handler so a regression in either
// the SQL filter OR the post-filter would fail it.
func TestListServerTransferReturnsOnlyCallerVisibleRowsForMember(t *testing.T) {
	cleanup := setupListServerTransferFixture(t)
	defer cleanup()

	// alice=100, bob=200, charlie=300. Seed five rows covering every position
	// a member could occupy plus one row the member must NEVER see.
	aliceFrom := seedTerminalTransfer(t, 1, 100, 200, 100)
	aliceTo := seedTerminalTransfer(t, 2, 300, 100, 300)
	aliceInitiated := seedTerminalTransfer(t, 3, 200, 300, 100)
	bobAlone := seedTerminalTransfer(t, 4, 200, 300, 200)
	charlieAlone := seedTerminalTransfer(t, 5, 300, 200, 300)

	ids := callListServerTransfer(t, 100, model.RoleMember)

	assert.ElementsMatch(t,
		[]uint64{aliceFrom, aliceTo, aliceInitiated},
		ids,
		"member must see exactly the rows where they are From/To/Initiator",
	)
	for _, forbidden := range []uint64{bobAlone, charlieAlone} {
		assert.NotContains(t, ids, forbidden, "member must never see a row they do not participate in")
	}
}

// Admin sees every row. This pins down that the SQL-level filter is gated on
// role and is not accidentally applied to admins (which would be a regression
// in the other direction).
func TestListServerTransferReturnsEveryRowForAdmin(t *testing.T) {
	cleanup := setupListServerTransferFixture(t)
	defer cleanup()

	a := seedTerminalTransfer(t, 1, 100, 200, 100)
	b := seedTerminalTransfer(t, 2, 200, 300, 200)
	c := seedTerminalTransfer(t, 3, 300, 100, 300)

	ids := callListServerTransfer(t, 999, model.RoleAdmin)

	assert.ElementsMatch(t, []uint64{a, b, c}, ids, "admin sees every row")
}

// Pin down the optimisation directly: the SELECT that hits the audit table
// for a non-admin caller MUST include a per-user WHERE clause. The behavioural
// tests above would pass even if the SQL stayed `SELECT *` (the post-filter
// hides forbidden rows), so they cannot regress-detect the perf fix going
// away. This test captures the executed SQL and asserts the filter is pushed
// down to the database.
//
// Why pinning the optimisation matters: ServerTransfer is the only listX
// endpoint that hits the DB (the rest serve in-memory caches) and it grows
// unbounded as an audit log. Without SQL-side filtering, every member's page
// load scans the entire historical table. A future refactor that drops the
// per-caller WHERE clause would not be caught by any behavioural assertion —
// hence this explicit guard.
func TestListServerTransferPushesPerCallerFilterIntoSQLForMember(t *testing.T) {
	cleanup, captured := setupListServerTransferFixtureWithSQLCapture(t)
	defer cleanup()

	seedTerminalTransfer(t, 1, 100, 200, 100)
	seedTerminalTransfer(t, 2, 200, 300, 200)

	_ = callListServerTransfer(t, 100, model.RoleMember)

	stmt := findSelectAgainstServerTransfers(captured.Snapshot())
	require.NotEmpty(t, stmt, "expected a SELECT against server_transfers to be issued")
	low := strings.ToLower(stmt)
	require.Contains(t, low, "where", "non-admin list must apply a per-caller WHERE filter at the SQL level")
	for _, col := range []string{"from_user_id", "to_user_id", "initiator_id"} {
		require.Contains(t, low, col, "WHERE clause must filter on %s", col)
	}
}

// The admin path must NOT push the per-user filter — admins see everything.
// Without this assertion a refactor that always applies the filter would
// silently hide cross-tenant rows from admins (an availability regression of
// the admin observability surface).
func TestListServerTransferOmitsPerCallerFilterForAdmin(t *testing.T) {
	cleanup, captured := setupListServerTransferFixtureWithSQLCapture(t)
	defer cleanup()

	seedTerminalTransfer(t, 1, 100, 200, 100)

	_ = callListServerTransfer(t, 999, model.RoleAdmin)

	stmt := findSelectAgainstServerTransfers(captured.Snapshot())
	require.NotEmpty(t, stmt, "expected a SELECT against server_transfers to be issued")
	low := strings.ToLower(stmt)
	for _, col := range []string{"from_user_id", "to_user_id", "initiator_id"} {
		require.NotContains(t, low, col, "admin list must NOT filter by %s — admins observe all transfers", col)
	}
}

func setupListServerTransferFixture(t *testing.T) func() {
	t.Helper()
	if singleton.Localizer == nil {
		singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	}
	originalDB := singleton.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&model.Server{}, &model.ServerTransfer{}))
	singleton.DB = db
	return func() { singleton.DB = originalDB }
}

// sqlCapture records every SQL statement gorm executes against the test DB.
// Used by the optimisation guards above to assert that listServerTransfer
// actually pushes its per-caller filter into the WHERE clause.
type sqlCapture struct {
	mu    sync.Mutex
	stmts []string
}

func (s *sqlCapture) record(stmt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stmts = append(s.stmts, stmt)
}

func (s *sqlCapture) Snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.stmts))
	copy(out, s.stmts)
	return out
}

func setupListServerTransferFixtureWithSQLCapture(t *testing.T) (func(), *sqlCapture) {
	t.Helper()
	cleanup := setupListServerTransferFixture(t)

	cap := &sqlCapture{}
	err := singleton.DB.Callback().Query().After("gorm:query").Register("test:capture_sql", func(tx *gorm.DB) {
		cap.record(tx.Statement.SQL.String())
	})
	require.NoError(t, err)

	return func() {
		_ = singleton.DB.Callback().Query().Remove("test:capture_sql")
		cleanup()
	}, cap
}

// findSelectAgainstServerTransfers returns the first captured SELECT whose
// FROM clause is `server_transfers`. We don't care about ordering callbacks
// or AutoMigrate scaffolding queries — only the handler's own SELECT.
func findSelectAgainstServerTransfers(stmts []string) string {
	for _, s := range stmts {
		low := strings.ToLower(s)
		if strings.HasPrefix(strings.TrimSpace(low), "select") && strings.Contains(low, "server_transfers") {
			return s
		}
	}
	return ""
}

func seedTerminalTransfer(t *testing.T, serverID, fromUserID, toUserID, initiatorID uint64) uint64 {
	t.Helper()
	tr := &model.ServerTransfer{
		ServerID:    serverID,
		FromUserID:  fromUserID,
		ToUserID:    toUserID,
		InitiatorID: initiatorID,
		Status:      model.ServerTransferStatusVerified,
	}
	assert.NoError(t, singleton.DB.Create(tr).Error)
	return tr.ID
}

func callListServerTransfer(t *testing.T, callerID uint64, role model.Role) []uint64 {
	t.Helper()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, callerID, role)
		c.Next()
	})
	r.GET("/transfer", listHandler(listServerTransfer))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/transfer", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Success bool                    `json:"success"`
		Data    []*model.ServerTransfer `json:"data"`
		Error   string                  `json:"error"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success, "list call must succeed: %s", resp.Error)

	ids := make([]uint64, 0, len(resp.Data))
	for _, tr := range resp.Data {
		ids = append(ids, tr.ID)
	}
	return ids
}
