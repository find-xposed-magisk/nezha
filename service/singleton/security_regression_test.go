package singleton

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
	"google.golang.org/grpc/metadata"
)

type capturedTaskStream struct {
	tasks chan *pb.Task
}

func newCapturedTaskStream() *capturedTaskStream {
	return &capturedTaskStream{tasks: make(chan *pb.Task, 4)}
}

func (s *capturedTaskStream) Send(task *pb.Task) error {
	s.tasks <- task
	return nil
}

func (s *capturedTaskStream) Recv() (*pb.TaskResult, error) { return nil, context.Canceled }
func (s *capturedTaskStream) SetHeader(metadata.MD) error   { return nil }
func (s *capturedTaskStream) SendHeader(metadata.MD) error  { return nil }
func (s *capturedTaskStream) SetTrailer(metadata.MD)        {}
func (s *capturedTaskStream) Context() context.Context      { return context.Background() }
func (s *capturedTaskStream) SendMsg(any) error             { return nil }
func (s *capturedTaskStream) RecvMsg(any) error             { return context.Canceled }

func replaceServerSharedForSecurityTest(t *testing.T, servers ...*model.Server) {
	t.Helper()

	original := ServerShared
	serverClass := &ServerClass{
		class: class[uint64, *model.Server]{
			list: make(map[uint64]*model.Server),
		},
		uuidToID: make(map[string]uint64),
	}
	for _, server := range servers {
		serverClass.list[server.ID] = server
	}
	ServerShared = serverClass
	t.Cleanup(func() { ServerShared = original })
}

func replaceUserInfoMapForSecurityTest(t *testing.T, users map[uint64]model.UserInfo) {
	t.Helper()

	UserLock.Lock()
	original := UserInfoMap
	UserInfoMap = users
	UserLock.Unlock()

	t.Cleanup(func() {
		UserLock.Lock()
		UserInfoMap = original
		UserLock.Unlock()
	})
}

func TestCronTriggerSkipsServersOwnedByOtherUsers(t *testing.T) {
	firstStream := newCapturedTaskStream()
	secondStream := newCapturedTaskStream()
	replaceServerSharedForSecurityTest(t,
		&model.Server{Common: model.Common{ID: 1, UserID: 100}, Name: "member-server", TaskStream: firstStream},
		&model.Server{Common: model.Common{ID: 2, UserID: 200}, Name: "admin-server", TaskStream: secondStream},
	)

	cronTask := &model.Cron{
		Common:  model.Common{ID: 99, UserID: 100},
		Command: "id",
		Cover:   model.CronCoverAll,
		Servers: []uint64{},
	}

	CronTrigger(cronTask)()

	assertTaskCommand(t, firstStream, "id")
	assertNoTask(t, secondStream)
}

func TestSendTriggerTasksSkipsCronOwnedByAnotherUser(t *testing.T) {
	attackerStream := newCapturedTaskStream()
	replaceServerSharedForSecurityTest(t,
		&model.Server{Common: model.Common{ID: 7, UserID: 200}, Name: "attacker-server", TaskStream: attackerStream},
	)
	replaceUserInfoMapForSecurityTest(t, map[uint64]model.UserInfo{
		1:   {Role: model.RoleAdmin},
		200: {Role: model.RoleMember},
	})

	adminCron := &model.Cron{
		Common:  model.Common{ID: 42, UserID: 1},
		Command: "admin-maintenance",
		Cover:   model.CronCoverAlertTrigger,
	}
	cronClass := &CronClass{
		class: class[uint64, *model.Cron]{
			list: map[uint64]*model.Cron{adminCron.ID: adminCron},
		},
	}

	cronClass.SendTriggerTasks([]uint64{adminCron.ID}, 7, 200)

	assertNoTask(t, attackerStream)
}

func assertTaskCommand(t *testing.T, stream *capturedTaskStream, expectedCommand string) {
	t.Helper()

	select {
	case task := <-stream.tasks:
		if task.GetType() != model.TaskTypeCommand {
			t.Fatalf("expected command task type, got %v", task.GetType())
		}
		if task.GetData() != expectedCommand {
			t.Fatalf("expected command %q, got %q", expectedCommand, task.GetData())
		}
	case <-time.After(time.Second):
		t.Fatalf("expected command %q to be sent", expectedCommand)
	}
}

func assertNoTask(t *testing.T, stream *capturedTaskStream) {
	t.Helper()

	select {
	case task := <-stream.tasks:
		t.Fatalf("expected no task to be sent, got command %q", task.GetData())
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCronTriggerSendsToMemberOwnedServer(t *testing.T) {
	memberStream := newCapturedTaskStream()
	replaceServerSharedForSecurityTest(t,
		&model.Server{Common: model.Common{ID: 1, UserID: 100}, Name: "member-server", TaskStream: memberStream},
	)
	replaceUserInfoMapForSecurityTest(t, map[uint64]model.UserInfo{
		100: {Role: model.RoleMember},
	})

	cronTask := &model.Cron{
		Common:  model.Common{ID: 99, UserID: 100},
		Command: "id",
		Cover:   model.CronCoverAll,
	}

	CronTrigger(cronTask)()

	assertTaskCommand(t, memberStream, "id")
}

func TestCronTriggerAdminCronFansOutAcrossOwners(t *testing.T) {
	first := newCapturedTaskStream()
	second := newCapturedTaskStream()
	replaceServerSharedForSecurityTest(t,
		&model.Server{Common: model.Common{ID: 1, UserID: 100}, Name: "member-server", TaskStream: first},
		&model.Server{Common: model.Common{ID: 2, UserID: 200}, Name: "admin-server", TaskStream: second},
	)
	replaceUserInfoMapForSecurityTest(t, map[uint64]model.UserInfo{
		1:   {Role: model.RoleAdmin},
		100: {Role: model.RoleMember},
		200: {Role: model.RoleAdmin},
	})

	cronTask := &model.Cron{
		Common:  model.Common{ID: 99, UserID: 1},
		Command: "maintenance",
		Cover:   model.CronCoverAll,
	}

	CronTrigger(cronTask)()

	assertTaskCommand(t, first, "maintenance")
	assertTaskCommand(t, second, "maintenance")
}

func TestCronTriggerLegacyZeroOwnerFansOut(t *testing.T) {
	first := newCapturedTaskStream()
	replaceServerSharedForSecurityTest(t,
		&model.Server{Common: model.Common{ID: 1, UserID: 100}, Name: "member-server", TaskStream: first},
	)
	replaceUserInfoMapForSecurityTest(t, map[uint64]model.UserInfo{
		100: {Role: model.RoleMember},
	})

	cronTask := &model.Cron{
		Common:  model.Common{ID: 99, UserID: 0},
		Command: "legacy",
		Cover:   model.CronCoverAll,
	}

	CronTrigger(cronTask)()

	assertTaskCommand(t, first, "legacy")
}

func TestCronTriggerSkipsServersWhenOwnerNotKnown(t *testing.T) {
	stream := newCapturedTaskStream()
	replaceServerSharedForSecurityTest(t,
		&model.Server{Common: model.Common{ID: 1, UserID: 100}, Name: "member-server", TaskStream: stream},
	)
	replaceUserInfoMapForSecurityTest(t, map[uint64]model.UserInfo{
		100: {Role: model.RoleMember},
	})

	cronTask := &model.Cron{
		Common:  model.Common{ID: 99, UserID: 999},
		Command: "ghost",
		Cover:   model.CronCoverAll,
	}

	CronTrigger(cronTask)()

	assertNoTask(t, stream)
}

func TestSendTriggerTasksAllowsSelfOwnedCron(t *testing.T) {
	stream := newCapturedTaskStream()
	replaceServerSharedForSecurityTest(t,
		&model.Server{Common: model.Common{ID: 7, UserID: 200}, Name: "member-server", TaskStream: stream},
	)
	replaceUserInfoMapForSecurityTest(t, map[uint64]model.UserInfo{
		200: {Role: model.RoleMember},
	})

	memberCron := &model.Cron{
		Common:  model.Common{ID: 42, UserID: 200},
		Command: "member-task",
		Cover:   model.CronCoverAlertTrigger,
	}
	cronClass := &CronClass{
		class: class[uint64, *model.Cron]{
			list: map[uint64]*model.Cron{memberCron.ID: memberCron},
		},
	}

	cronClass.SendTriggerTasks([]uint64{memberCron.ID}, 7, 200)

	assertTaskCommand(t, stream, "member-task")
}

func TestSendTriggerTasksAllowsAdminCallerToTriggerAny(t *testing.T) {
	stream := newCapturedTaskStream()
	replaceServerSharedForSecurityTest(t,
		&model.Server{Common: model.Common{ID: 9, UserID: 100}, Name: "any-server", TaskStream: stream},
	)
	replaceUserInfoMapForSecurityTest(t, map[uint64]model.UserInfo{
		1:   {Role: model.RoleAdmin},
		100: {Role: model.RoleMember},
	})

	memberCron := &model.Cron{
		Common:  model.Common{ID: 42, UserID: 100},
		Command: "member-task",
		Cover:   model.CronCoverAlertTrigger,
	}
	cronClass := &CronClass{
		class: class[uint64, *model.Cron]{
			list: map[uint64]*model.Cron{memberCron.ID: memberCron},
		},
	}

	cronClass.SendTriggerTasks([]uint64{memberCron.ID}, 9, 1)

	assertTaskCommand(t, stream, "member-task")
}

func TestSendTriggerTasksIgnoresUnknownTaskIDs(t *testing.T) {
	stream := newCapturedTaskStream()
	replaceServerSharedForSecurityTest(t,
		&model.Server{Common: model.Common{ID: 7, UserID: 200}, Name: "member-server", TaskStream: stream},
	)
	replaceUserInfoMapForSecurityTest(t, map[uint64]model.UserInfo{
		200: {Role: model.RoleMember},
	})

	cronClass := &CronClass{
		class: class[uint64, *model.Cron]{
			list: map[uint64]*model.Cron{},
		},
	}

	cronClass.SendTriggerTasks([]uint64{12345}, 7, 200)
	cronClass.SendTriggerTasks(nil, 7, 200)

	assertNoTask(t, stream)
}

func TestSendTriggerTasksMixedCronIDsOnlyFiresAllowed(t *testing.T) {
	stream := newCapturedTaskStream()
	replaceServerSharedForSecurityTest(t,
		&model.Server{Common: model.Common{ID: 7, UserID: 200}, Name: "member-server", TaskStream: stream},
	)
	replaceUserInfoMapForSecurityTest(t, map[uint64]model.UserInfo{
		1:   {Role: model.RoleAdmin},
		200: {Role: model.RoleMember},
	})

	memberCron := &model.Cron{
		Common:  model.Common{ID: 7, UserID: 200},
		Command: "member-task",
		Cover:   model.CronCoverAlertTrigger,
	}
	adminCron := &model.Cron{
		Common:  model.Common{ID: 8, UserID: 1},
		Command: "admin-task",
		Cover:   model.CronCoverAlertTrigger,
	}
	cronClass := &CronClass{
		class: class[uint64, *model.Cron]{
			list: map[uint64]*model.Cron{memberCron.ID: memberCron, adminCron.ID: adminCron},
		},
	}

	cronClass.SendTriggerTasks([]uint64{memberCron.ID, adminCron.ID}, 7, 200)

	select {
	case task := <-stream.tasks:
		if task.GetData() != "member-task" {
			t.Fatalf("expected member-task, got %q", task.GetData())
		}
	case <-time.After(time.Second):
		t.Fatalf("expected member-task to be sent")
	}
	assertNoTask(t, stream)
}

func TestClassCheckPermission(t *testing.T) {
	replaceUserInfoMapForSecurityTest(t, map[uint64]model.UserInfo{
		1:   {Role: model.RoleAdmin},
		200: {Role: model.RoleMember},
	})
	sharedClass := &ServerClass{
		class: class[uint64, *model.Server]{
			list: map[uint64]*model.Server{
				1: {Common: model.Common{ID: 1, UserID: 200}},
				2: {Common: model.Common{ID: 2, UserID: 1}},
			},
		},
		uuidToID: map[string]uint64{},
	}

	memberCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	memberCtx.Set(model.CtxKeyAuthorizedUser, &model.User{
		Common: model.Common{ID: 200},
		Role:   model.RoleMember,
	})
	adminCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	adminCtx.Set(model.CtxKeyAuthorizedUser, &model.User{
		Common: model.Common{ID: 1},
		Role:   model.RoleAdmin,
	})

	if !sharedClass.CheckPermission(memberCtx, slices.Values([]uint64{1})) {
		t.Fatal("expected member to access own resource")
	}
	if sharedClass.CheckPermission(memberCtx, slices.Values([]uint64{2})) {
		t.Fatal("expected member to be denied foreign resource")
	}
	if !sharedClass.CheckPermission(memberCtx, slices.Values([]uint64{})) {
		t.Fatal("expected empty iterator to be allowed")
	}
	if !sharedClass.CheckPermission(memberCtx, slices.Values([]uint64{999})) {
		t.Fatal("expected unknown id to be ignored (vacuous true)")
	}
	if !sharedClass.CheckPermission(adminCtx, slices.Values([]uint64{1, 2})) {
		t.Fatal("expected admin to access any resource")
	}
}
