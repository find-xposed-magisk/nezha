package singleton

import (
	"testing"
	"time"

	"github.com/nezhahq/nezha/model"
)

func newNotificationClassWithItems(items ...*model.Notification) *NotificationClass {
	nc := NewEmptyNotificationClassForTest()
	for _, item := range items {
		nc.InsertForTest(item)
	}
	return nc
}

func TestNotificationClassDeleteGroupCleansReverseIndex(t *testing.T) {
	n := &model.Notification{Common: model.Common{ID: 1, UserID: 2}, Name: "hook"}
	nc := newNotificationClassWithItems(n)
	group := &model.NotificationGroup{Common: model.Common{ID: 10, UserID: 2}, Name: "group"}
	nc.UpdateGroup(group, []uint64{n.ID})

	nc.DeleteGroup([]uint64{group.ID})

	if _, ok := nc.groupToIDList[group.ID]; ok {
		t.Fatalf("forward index still contains deleted group %d", group.ID)
	}
	if groups, ok := nc.idToGroupList[n.ID]; ok {
		if _, stale := groups[group.ID]; stale {
			t.Fatalf("reverse index for notification %d still contains deleted group %d", n.ID, group.ID)
		}
	}
}

func TestNotificationClassUpdateGroupCleansRemovedMembership(t *testing.T) {
	first := &model.Notification{Common: model.Common{ID: 1, UserID: 2}, Name: "first"}
	second := &model.Notification{Common: model.Common{ID: 2, UserID: 2}, Name: "second"}
	nc := newNotificationClassWithItems(first, second)
	group := &model.NotificationGroup{Common: model.Common{ID: 10, UserID: 2}, Name: "group"}
	nc.UpdateGroup(group, []uint64{first.ID, second.ID})

	nc.UpdateGroup(group, []uint64{second.ID})

	if groups, ok := nc.idToGroupList[first.ID]; ok {
		if _, stale := groups[group.ID]; stale {
			t.Fatalf("reverse index for notification %d still contains group %d", first.ID, group.ID)
		}
	}
	if _, ok := nc.idToGroupList[second.ID][group.ID]; !ok {
		t.Fatalf("reverse index lost retained membership of notification %d in group %d", second.ID, group.ID)
	}
}

func TestNotificationClassUpdateRepairsOrphanedReverseIndex(t *testing.T) {
	n := &model.Notification{Common: model.Common{ID: 1, UserID: 2}, Name: "hook"}
	nc := newNotificationClassWithItems(n)
	nc.idToGroupList[n.ID] = map[uint64]struct{}{99: {}}

	n.Name = "updated"
	nc.Update(n)

	if groups, ok := nc.idToGroupList[n.ID]; ok && len(groups) != 0 {
		t.Fatalf("orphaned reverse memberships were not removed: %v", groups)
	}
}

func TestNotificationClassGroupMutationsDoNotDeadlock(t *testing.T) {
	nc := NewEmptyNotificationClassForTest()
	nc.listMu.RLock()
	readLockHeld := true
	defer func() {
		if readLockHeld {
			nc.listMu.RUnlock()
		}
	}()

	deleteDone := make(chan struct{})
	go func() {
		nc.DeleteGroup([]uint64{10})
		close(deleteDone)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for nc.listMu.TryRLock() {
		nc.listMu.RUnlock()
		if time.Now().After(deadline) {
			t.Fatal("DeleteGroup did not queue for listMu")
		}
		time.Sleep(time.Millisecond)
	}

	updateDone := make(chan struct{})
	go func() {
		nc.UpdateGroup(&model.NotificationGroup{
			Common: model.Common{ID: 10, UserID: 2},
			Name:   "group",
		}, nil)
		close(updateDone)
	}()

	deadline = time.Now().Add(2 * time.Second)
	for nc.groupMu.TryLock() {
		nc.groupMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("neither group mutation acquired groupMu")
		}
		time.Sleep(time.Millisecond)
	}

	nc.listMu.RUnlock()
	readLockHeld = false

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for deleteDone != nil || updateDone != nil {
		select {
		case <-deleteDone:
			deleteDone = nil
		case <-updateDone:
			updateDone = nil
		case <-timer.C:
			t.Fatal("concurrent UpdateGroup and DeleteGroup deadlocked")
		}
	}
}
