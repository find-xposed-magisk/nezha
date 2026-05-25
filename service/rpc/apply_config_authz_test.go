package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/nezhahq/nezha/service/singleton"
)

// A malicious or buggy agent owning server A must NOT be able to fail a
// ServerTransfer row belonging to server B by reporting a TaskResult whose
// Id is set to B's transfer ID. The agent-task-result authorization
// invariant (commit 02129f1) requires the dashboard to verify the result's
// addressed object actually belongs to the reporting agent before acting
// on it. Without the cross-check, any compromised agent could cancel/fail
// every in-flight transfer in the system.
func TestRequestTaskApplyConfigIgnoresForeignTransferFailure(t *testing.T) {
	// Two distinct servers with different owners. attackerSrv reports the
	// failure; victimSrv is the one a pending transfer points at.
	attackerSrv := &model.Server{
		Common: model.Common{ID: 7, UserID: 100},
		UUID:   "cccccccc-cccc-cccc-cccc-cccccccccccc",
		Name:   "attacker",
	}
	victimSrv := &model.Server{
		Common: model.Common{ID: 8, UserID: 200},
		UUID:   "dddddddd-dddd-dddd-dddd-dddddddddddd",
		Name:   "victim",
	}
	users := map[uint64]model.UserInfo{
		100: {Role: model.RoleMember},
		200: {Role: model.RoleMember},
		300: {Role: model.RoleMember, AgentSecret: "to-user-secret"},
	}
	secrets := map[string]uint64{
		"attacker-secret": 100,
		"to-user-secret":  300,
	}
	setupApplyConfigAuthzFixture(t, []*model.Server{attackerSrv, victimSrv}, users, secrets)

	// Pending transfer for victimSrv (200 -> 300). attackerSrv is unrelated.
	tr := initiateAndRegisterPendingTransfer(t, victimSrv.ID, 200, 300, 1)

	// Attacker reports a failed ApplyConfig carrying the victim's transfer ID.
	runApplyConfigAuthzResult(t, "attacker-secret", attackerSrv.UUID, &pb.TaskResult{
		Id:         tr.ID,
		Type:       model.TaskTypeServerTransferApply,
		Successful: false,
		Data:       "spoofed failure",
	})

	var refreshed model.ServerTransfer
	if err := singleton.DB.First(&refreshed, tr.ID).Error; err != nil {
		t.Fatalf("re-read transfer: %v", err)
	}
	if refreshed.Status != model.ServerTransferStatusPending {
		t.Fatalf("foreign-server ApplyConfig failure must leave transfer Pending, got status=%d last_error=%q",
			refreshed.Status, refreshed.LastError)
	}

	var vs model.Server
	if err := singleton.DB.First(&vs, victimSrv.ID).Error; err != nil {
		t.Fatalf("re-read victim server: %v", err)
	}
	if vs.UserID != 300 {
		t.Fatalf("victim server ownership must remain at ToUserID, got %d", vs.UserID)
	}
}

// The legitimate path must still mark the transfer Failed: the reporter is
// the actual transfer subject. This guards against an over-tight ownership
// check that would also break the working flow.
func TestRequestTaskApplyConfigAcceptsOwnTransferFailure(t *testing.T) {
	srv := &model.Server{
		Common: model.Common{ID: 9, UserID: 200},
		UUID:   "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee",
		Name:   "subject",
	}
	users := map[uint64]model.UserInfo{
		200: {Role: model.RoleMember, AgentSecret: "from-user-secret"},
		300: {Role: model.RoleMember, AgentSecret: "to-user-secret"},
	}
	secrets := map[string]uint64{
		// During Pending the agent still authenticates with the previous
		// owner's secret — that's exactly the auth-tolerance window the
		// transfer feature exists for.
		"from-user-secret": 200,
		"to-user-secret":   300,
	}
	setupApplyConfigAuthzFixture(t, []*model.Server{srv}, users, secrets)

	tr := initiateAndRegisterPendingTransfer(t, srv.ID, 200, 300, 1)

	runApplyConfigAuthzResult(t, "from-user-secret", srv.UUID, &pb.TaskResult{
		Id:         tr.ID,
		Type:       model.TaskTypeServerTransferApply,
		Successful: false,
		Data:       "DisableCommandExecute=true",
	})

	var refreshed model.ServerTransfer
	if err := singleton.DB.First(&refreshed, tr.ID).Error; err != nil {
		t.Fatalf("re-read transfer: %v", err)
	}
	if refreshed.Status != model.ServerTransferStatusFailed {
		t.Fatalf("own-server ApplyConfig failure must mark transfer Failed, got status=%d", refreshed.Status)
	}
}

func TestRequestTaskCancelledTransferAllowsForwardHandshakeReconnectForRevert(t *testing.T) {
	srv := &model.Server{
		Common: model.Common{ID: 12, UserID: 200},
		UUID:   "12121212-1212-1212-1212-121212121212",
		Name:   "cancelled-revert",
	}
	users := map[uint64]model.UserInfo{
		200: {Role: model.RoleMember, AgentSecret: "cancel-from-secret"},
		300: {Role: model.RoleMember, AgentSecret: "cancel-to-secret"},
	}
	secrets := map[string]uint64{
		"cancel-from-secret": 200,
		"cancel-to-secret":   300,
	}
	setupApplyConfigAuthzFixture(t, []*model.Server{srv}, users, secrets)

	tr := initiateAndRegisterPendingTransfer(t, srv.ID, 200, 300, 1)
	forward := tr.HandshakeSecret
	if forward == "" {
		t.Fatal("precondition: pending transfer must carry a forward HandshakeSecret")
	}
	if _, err := singleton.ServerTransferShared.Cancel(tr.ID); err != nil {
		t.Fatalf("cancel transfer: %v", err)
	}

	sent := runApplyConfigAuthzReconnect(t, forward, srv.UUID)
	if len(sent) != 1 {
		t.Fatalf("expected one revert ApplyConfig task, got %d", len(sent))
	}
	if sent[0].Type != model.TaskTypeServerTransferApply {
		t.Fatalf("expected ApplyConfig task, got type=%d", sent[0].Type)
	}
	var settled model.ServerTransfer
	if err := singleton.DB.First(&settled, tr.ID).Error; err != nil {
		t.Fatalf("reload transfer: %v", err)
	}
	if !strings.Contains(sent[0].Data, settled.RevertHandshakeSecret) {
		t.Fatalf("cancelled transfer rollback must push the per-transfer RevertHandshakeSecret, got payload %q", sent[0].Data)
	}
	if strings.Contains(sent[0].Data, "cancel-from-secret") || strings.Contains(sent[0].Data, "cancel-to-secret") {
		t.Fatalf("user-global AgentSecrets must never appear in transfer payloads, got %q", sent[0].Data)
	}
}

func TestRequestTaskTimedOutTransferAllowsForwardHandshakeReconnectForRevert(t *testing.T) {
	srv := &model.Server{
		Common: model.Common{ID: 17, UserID: 200},
		UUID:   "17171717-1717-1717-1717-171717171717",
		Name:   "timeout-revert",
	}
	users := map[uint64]model.UserInfo{
		200: {Role: model.RoleMember, AgentSecret: "timeout-from-secret"},
		300: {Role: model.RoleMember, AgentSecret: "timeout-to-secret"},
	}
	secrets := map[string]uint64{
		"timeout-from-secret": 200,
		"timeout-to-secret":   300,
	}
	setupApplyConfigAuthzFixture(t, []*model.Server{srv}, users, secrets)

	tr := initiateAndRegisterPendingTransfer(t, srv.ID, 200, 300, 1)
	forward := tr.HandshakeSecret
	if forward == "" {
		t.Fatal("precondition: pending transfer must carry a forward HandshakeSecret")
	}
	staleUpdatedAt := time.Now().Add(-25 * time.Hour)
	if err := singleton.DB.Model(&model.ServerTransfer{}).
		Where("id = ?", tr.ID).
		UpdateColumn("updated_at", staleUpdatedAt).Error; err != nil {
		t.Fatalf("stale transfer update: %v", err)
	}
	if _, err := singleton.ServerTransferShared.MarkTimeout(tr.ID); err != nil {
		t.Fatalf("timeout transfer: %v", err)
	}

	sent := runApplyConfigAuthzReconnect(t, forward, srv.UUID)
	if len(sent) != 1 {
		t.Fatalf("expected one timeout revert ApplyConfig task, got %d", len(sent))
	}
	var settled model.ServerTransfer
	if err := singleton.DB.First(&settled, tr.ID).Error; err != nil {
		t.Fatalf("reload transfer: %v", err)
	}
	if !strings.Contains(sent[0].Data, settled.RevertHandshakeSecret) {
		t.Fatalf("timeout rollback must push the per-transfer RevertHandshakeSecret, got payload %q", sent[0].Data)
	}
	if strings.Contains(sent[0].Data, "timeout-from-secret") || strings.Contains(sent[0].Data, "timeout-to-secret") {
		t.Fatalf("user-global AgentSecrets must never appear in transfer payloads, got %q", sent[0].Data)
	}
}

func TestRequestTaskRejectsToUserGlobalSecretEvenWithLiveRevertDelivery(t *testing.T) {
	srv := &model.Server{
		Common: model.Common{ID: 16, UserID: 200},
		UUID:   "16161616-1616-1616-1616-161616161616",
		Name:   "to-user-global-rejected",
	}
	users := map[uint64]model.UserInfo{
		200: {Role: model.RoleMember, AgentSecret: "rejected-from-secret"},
		300: {Role: model.RoleMember, AgentSecret: "rejected-to-secret"},
	}
	secrets := map[string]uint64{
		"rejected-from-secret": 200,
		"rejected-to-secret":   300,
	}
	setupApplyConfigAuthzFixture(t, []*model.Server{srv}, users, secrets)

	tr := initiateAndRegisterPendingTransfer(t, srv.ID, 200, 300, 1)
	if _, err := singleton.ServerTransferShared.Cancel(tr.ID); err != nil {
		t.Fatalf("cancel transfer: %v", err)
	}
	if _, ok := singleton.ServerTransferShared.LookupRevertDelivery(srv.ID); !ok {
		t.Fatal("precondition: cancel must register a revert delivery")
	}

	sent := 0
	stream := &requestTaskSecurityStream{
		ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
			"client_secret", "rejected-to-secret",
			"client_uuid", srv.UUID,
		)),
		onSend: func(*pb.Task) {
			sent++
		},
	}
	if err := NewNezhaHandler().RequestTask(stream); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("ToUserID global AgentSecret must never authenticate via revert recovery; PushIfOnline only delivers per-transfer secrets to the real agent")
	}
	if sent != 0 {
		t.Fatalf("rejected ToUserID auth must not trigger any ApplyConfig push, got %d sends", sent)
	}
	if _, ok := singleton.ServerTransferShared.LookupRevertDelivery(srv.ID); !ok {
		t.Fatal("rejected ToUserID auth must not consume the revert delivery — the real agent still needs it for the eventual per-transfer recovery")
	}
}

// Whether or not a revert delivery is still in flight, the destination
// user's global AgentSecret must be rejected on every auth path —
// PushIfOnline never sends that secret to the agent so a reconnect under
// it cannot come from the real agent. This pins the post-fix invariant.
func TestReportSystemInfoRejectsCancelledTransferToUserSecret(t *testing.T) {
	srv := &model.Server{
		Common: model.Common{ID: 15, UserID: 200},
		UUID:   "15151515-1515-1515-1515-151515151515",
		Name:   "cancelled-report",
	}
	users := map[uint64]model.UserInfo{
		200: {Role: model.RoleMember, AgentSecret: "report-from-secret"},
		300: {Role: model.RoleMember, AgentSecret: "report-to-secret"},
	}
	secrets := map[string]uint64{
		"report-from-secret": 200,
		"report-to-secret":   300,
	}
	setupApplyConfigAuthzFixture(t, []*model.Server{srv}, users, secrets)

	tr := initiateAndRegisterPendingTransfer(t, srv.ID, 200, 300, 1)
	if _, err := singleton.ServerTransferShared.Cancel(tr.ID); err != nil {
		t.Fatalf("cancel transfer: %v", err)
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"client_secret", "report-to-secret",
		"client_uuid", srv.UUID,
	))
	if _, err := NewNezhaHandler().ReportSystemInfo(ctx, &pb.Host{}); err == nil {
		t.Fatal("ReportSystemInfo must reject the destination user's global AgentSecret during revert recovery; PushIfOnline never delivers that credential to the real agent")
	}
	if _, ok := singleton.ServerTransferShared.LookupRevertDelivery(srv.ID); !ok {
		t.Fatal("rejected non-RequestTask auth must not consume the revert delivery")
	}
}

func setupApplyConfigAuthzFixture(t *testing.T, servers []*model.Server, users map[uint64]model.UserInfo, agentSecrets map[string]uint64) {
	t.Helper()

	originalDB := singleton.DB
	originalConf := singleton.Conf
	originalLoc := singleton.Loc
	originalServerShared := singleton.ServerShared
	originalUserInfoMap := singleton.UserInfoMap
	originalAgentSecretToUserID := singleton.AgentSecretToUserId
	originalServerTransferShared := singleton.ServerTransferShared

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)

	singleton.DB = db
	singleton.Conf = &singleton.ConfigClass{Config: &model.Config{}}
	singleton.Loc = time.UTC
	if err := singleton.DB.AutoMigrate(model.Server{}, model.ServerTransfer{}); err != nil {
		t.Fatal(err)
	}
	for _, server := range servers {
		if err := singleton.DB.Create(server).Error; err != nil {
			t.Fatal(err)
		}
	}

	singleton.UserLock.Lock()
	singleton.UserInfoMap = users
	singleton.AgentSecretToUserId = agentSecrets
	singleton.UserLock.Unlock()
	singleton.ServerShared = singleton.NewServerClass()
	for _, server := range servers {
		model.InitServer(server)
		singleton.ServerShared.Update(server, server.UUID)
	}
	singleton.ServerTransferShared = singleton.NewServerTransferClass()

	t.Cleanup(func() {
		if singleton.ServerTransferShared != nil {
			singleton.ServerTransferShared.Stop()
		}
		sqlDB.Close()
		singleton.DB = originalDB
		singleton.Conf = originalConf
		singleton.Loc = originalLoc
		singleton.ServerShared = originalServerShared
		singleton.ServerTransferShared = originalServerTransferShared
		singleton.UserLock.Lock()
		singleton.UserInfoMap = originalUserInfoMap
		singleton.AgentSecretToUserId = originalAgentSecretToUserID
		singleton.UserLock.Unlock()
	})
}

func initiateAndRegisterPendingTransfer(t *testing.T, serverID, fromUserID, toUserID, initiatorID uint64) *model.ServerTransfer {
	t.Helper()
	var created *model.ServerTransfer
	err := singleton.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = singleton.ServerTransferShared.Initiate(tx, serverID, fromUserID, toUserID, initiatorID)
		return err
	})
	if err != nil {
		t.Fatalf("initiate transfer: %v", err)
	}
	singleton.ServerTransferShared.Register(created)
	return created
}

func runApplyConfigAuthzResult(t *testing.T, secret, uuid string, result *pb.TaskResult) {
	t.Helper()
	stream := &requestTaskSecurityStream{
		ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
			"client_secret", secret,
			"client_uuid", uuid,
		)),
		results: []*pb.TaskResult{result},
	}
	err := NewNezhaHandler().RequestTask(stream)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected RequestTask to finish after test result, got %v", err)
	}
}

func runApplyConfigAuthzReconnect(t *testing.T, secret, uuid string) []*pb.Task {
	t.Helper()
	var sent []*pb.Task
	stream := &requestTaskSecurityStream{
		ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
			"client_secret", secret,
			"client_uuid", uuid,
		)),
		onSend: func(task *pb.Task) {
			sent = append(sent, task)
		},
	}
	err := NewNezhaHandler().RequestTask(stream)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected RequestTask to finish after reconnect probe, got %v", err)
	}
	return sent
}

// Finding B regression: during the agent's 10s delayed ApplyConfig swap
// window, the agent still talks to the dashboard with the OLD (FromUserID)
// secret. After a cancel/fail/timeout, a registered revert delivery is the
// only signal that lets the eventually-arriving new-secret reconnect
// recover. The previous implementation cleared revertDeliveries from ANY
// successful old-secret authentication — including ReportSystemInfo2 from
// the periodic reportHost path — so a single old-secret RPC during the
// timer window could destroy the rollback record before the agent ever
// actually swapped secrets. Clearing the delivery is only safe when the
// auth call also gets a chance to consume it by pushing the rollback,
// which only the RequestTask handler does via OnAgentReconnect.
func TestReportSystemInfoDoesNotClearRevertDeliveryForOldSecret(t *testing.T) {
	srv := &model.Server{
		Common: model.Common{ID: 23, UserID: 200},
		UUID:   "23232323-2323-2323-2323-232323232323",
		Name:   "preserve-revert-delivery",
	}
	users := map[uint64]model.UserInfo{
		200: {Role: model.RoleMember, AgentSecret: "from-secret-23"},
		300: {Role: model.RoleMember, AgentSecret: "to-secret-23"},
	}
	secrets := map[string]uint64{
		"from-secret-23": 200,
		"to-secret-23":   300,
	}
	setupApplyConfigAuthzFixture(t, []*model.Server{srv}, users, secrets)

	tr := initiateAndRegisterPendingTransfer(t, srv.ID, 200, 300, 1)
	if _, err := singleton.ServerTransferShared.Cancel(tr.ID); err != nil {
		t.Fatalf("cancel transfer: %v", err)
	}
	if _, ok := singleton.ServerTransferShared.LookupRevertDelivery(srv.ID); !ok {
		t.Fatal("precondition: cancel must have registered a revert delivery")
	}

	// Simulate the agent's periodic reportHost calling ReportSystemInfo2
	// with the still-current (FromUserID) secret during the 10s pending
	// ApplyConfig window. Must succeed (server already reverted to
	// FromUserID) but must NOT clear the revert delivery — the agent has
	// not yet swapped secrets, and destroying the only recovery record
	// now would lock the agent out once its timer fires.
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"client_secret", "from-secret-23",
		"client_uuid", srv.UUID,
	))
	if _, err := NewNezhaHandler().ReportSystemInfo2(ctx, &pb.Host{}); err != nil {
		t.Fatalf("ReportSystemInfo2 with old (FromUserID) secret must succeed after revert, got %v", err)
	}

	if _, ok := singleton.ServerTransferShared.LookupRevertDelivery(srv.ID); !ok {
		t.Fatal("non-RequestTask auth with old secret must NOT clear revert delivery; it cannot push the rollback, so destroying the record locks out the eventually-switched agent")
	}
}

// Regression: when cancel/fail/timeout happens while the agent is offline
// (its only TaskStream is gone), pushRevertIfOnline is a no-op and the
// revertDelivery is the only signal we have left. The agent will reconnect
// *with the original FromUserID secret* (its in-memory liveCredentials still
// points at the secret it had before the swap), and that very reconnect must
// be the one that delivers the rollback ApplyConfig — otherwise the agent's
// 10s reload timer eventually commits the new secret and the dashboard, which
// already restored ownership to FromUserID, rejects every subsequent connect.
//
// The previous implementation cleared the revertDelivery inside
// authorizeAgentForUUIDWithRevertRecovery *before* RequestTask reached
// OnAgentReconnect, so the rollback push that OnAgentReconnect relies on
// (LookupRevertDelivery → pushRevertIfOnline) found nothing and the agent
// got no rollback at all.
func TestRequestTaskCancelledTransferDeliversRollbackOnOldSecretReconnect(t *testing.T) {
	srv := &model.Server{
		Common: model.Common{ID: 24, UserID: 200},
		UUID:   "24242424-2424-2424-2424-242424242424",
		Name:   "old-secret-rollback",
	}
	users := map[uint64]model.UserInfo{
		200: {Role: model.RoleMember, AgentSecret: "rollback-from-secret"},
		300: {Role: model.RoleMember, AgentSecret: "rollback-to-secret"},
	}
	secrets := map[string]uint64{
		"rollback-from-secret": 200,
		"rollback-to-secret":   300,
	}
	setupApplyConfigAuthzFixture(t, []*model.Server{srv}, users, secrets)

	tr := initiateAndRegisterPendingTransfer(t, srv.ID, 200, 300, 1)
	// Cancel while the agent is offline — the in-memory TaskStream is nil
	// (we never attached one), so pushRevertIfOnline silently no-ops.
	if _, err := singleton.ServerTransferShared.Cancel(tr.ID); err != nil {
		t.Fatalf("cancel transfer: %v", err)
	}
	if _, ok := singleton.ServerTransferShared.LookupRevertDelivery(srv.ID); !ok {
		t.Fatal("precondition: cancel while offline must leave a revert delivery for the eventual reconnect")
	}

	// Agent now reconnects with its original FromUserID secret (it never
	// received the new-secret ApplyConfig because it was offline). This
	// RequestTask must deliver the rollback so the agent's reload timer
	// supersedes onto the correct credential.
	sent := runApplyConfigAuthzReconnect(t, "rollback-from-secret", srv.UUID)
	if len(sent) != 1 {
		t.Fatalf("expected one rollback ApplyConfig task on old-secret reconnect, got %d", len(sent))
	}
	var settled model.ServerTransfer
	if err := singleton.DB.First(&settled, tr.ID).Error; err != nil {
		t.Fatalf("reload transfer: %v", err)
	}
	if !strings.Contains(sent[0].Data, settled.RevertHandshakeSecret) {
		t.Fatalf("old-secret reconnect rollback must carry the per-transfer RevertHandshakeSecret, got %q", sent[0].Data)
	}
	if strings.Contains(sent[0].Data, "rollback-from-secret") || strings.Contains(sent[0].Data, "rollback-to-secret") {
		t.Fatalf("user-global AgentSecrets must never appear in transfer payloads, got %q", sent[0].Data)
	}
}

// FORWARD-RECOVERY end-to-end: the exact production scenario the fix
// targets. PushIfOnline only ever delivers t.HandshakeSecret, the agent's
// 10s timer commits it to disk, the operator Cancels in that 10s window
// (revert push misses because the stream had no agent yet, or arrived
// before the forward apply finished). The agent reconnects with the
// forward HandshakeSecret it has on disk. RequestTask MUST accept that
// auth and then deliver one rollback ApplyConfig carrying the per-transfer
// RevertHandshakeSecret so the agent's next reload rotates onto the correct
// credential. Without this, the agent has no path back into the dashboard.
func TestRequestTaskForwardHandshakeSecretReconnectAfterCancelDeliversRollback(t *testing.T) {
	srv := &model.Server{
		Common: model.Common{ID: 31, UserID: 200},
		UUID:   "31313131-3131-3131-3131-313131313131",
		Name:   "forward-recovery",
	}
	users := map[uint64]model.UserInfo{
		200: {Role: model.RoleMember, AgentSecret: "fr-from-secret"},
		300: {Role: model.RoleMember, AgentSecret: "fr-to-secret"},
	}
	secrets := map[string]uint64{
		"fr-from-secret": 200,
		"fr-to-secret":   300,
	}
	setupApplyConfigAuthzFixture(t, []*model.Server{srv}, users, secrets)

	tr := initiateAndRegisterPendingTransfer(t, srv.ID, 200, 300, 1)
	forward := tr.HandshakeSecret
	if forward == "" {
		t.Fatal("precondition: pending transfer must carry a forward HandshakeSecret")
	}

	if _, err := singleton.ServerTransferShared.Cancel(tr.ID); err != nil {
		t.Fatalf("dashboard Cancel must succeed: %v", err)
	}

	sent := runApplyConfigAuthzReconnect(t, forward, srv.UUID)
	if len(sent) != 1 {
		t.Fatalf("forward-secret reconnect after Cancel must deliver one rollback ApplyConfig task, got %d", len(sent))
	}
	var settled model.ServerTransfer
	if err := singleton.DB.First(&settled, tr.ID).Error; err != nil {
		t.Fatalf("reload transfer: %v", err)
	}
	if !strings.Contains(sent[0].Data, settled.RevertHandshakeSecret) {
		t.Fatalf("rollback delivered after forward-secret recovery must carry the per-transfer RevertHandshakeSecret, got %q", sent[0].Data)
	}
	if strings.Contains(sent[0].Data, "fr-from-secret") || strings.Contains(sent[0].Data, "fr-to-secret") {
		t.Fatalf("user-global AgentSecrets must never appear in transfer payloads, got %q", sent[0].Data)
	}
}
