package singleton

import (
	"context"
	"testing"
	"time"

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
