package singleton

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/i18n"
)

func setupOnUserDeleteFixture(t *testing.T) (*ServerTransferClass, func()) {
	t.Helper()
	c, transferCleanup := setupTransferFixture(t)

	require.NoError(t, DB.AutoMigrate(&model.Cron{}, &model.Transfer{}, &model.ServerGroupServer{}))
	originalCronShared := CronShared
	CronShared = &CronClass{
		class: class[uint64, *model.Cron]{list: map[uint64]*model.Cron{}},
	}

	originalLocalizer := Localizer
	Localizer = i18n.NewLocalizer("zh_CN", domain, "translations", i18n.Translations)

	cleanup := func() {
		Localizer = originalLocalizer
		CronShared = originalCronShared
		transferCleanup()
	}
	return c, cleanup
}

func TestOnUserDeleteCancelsPendingTransfersAwayFromDeletedUser(t *testing.T) {
	c, cleanup := setupOnUserDeleteFixture(t)
	defer cleanup()

	const fromUser = uint64(100)
	const toUser = uint64(200)
	const serverID = uint64(1)

	seedServerForTransfer(t, serverID, fromUser)

	require.NoError(t, DB.AutoMigrate(&model.User{}))
	require.NoError(t, DB.Create(&model.User{
		Common:      model.Common{ID: fromUser},
		Username:    "alice",
		AgentSecret: "alice-secret",
	}).Error)
	require.NoError(t, DB.Create(&model.User{
		Common:      model.Common{ID: toUser},
		Username:    "bob",
		AgentSecret: "bob-secret",
	}).Error)
	UserLock.Lock()
	UserInfoMap[fromUser] = model.UserInfo{Role: model.RoleMember, AgentSecret: "alice-secret"}
	UserInfoMap[toUser] = model.UserInfo{Role: model.RoleMember, AgentSecret: "bob-secret"}
	UserLock.Unlock()

	tr := initiateAndRegister(t, c, serverID, fromUser, toUser, fromUser)
	require.True(t, c.HasPending(serverID), "precondition: pending transfer published")

	srv, ok := ServerShared.Get(serverID)
	require.True(t, ok)
	require.Equal(t, toUser, srv.GetUserID(), "precondition: pending transfer flipped owner to ToUserID")

	require.NoError(t, OnUserDelete([]uint64{fromUser}, func(format string, args ...any) error {
		return nil
	}))

	if c.HasPending(serverID) {
		t.Fatal("OnUserDelete on the transfer FromUserID must terminate the pending transfer so a later Cancel/Fail/Timeout cannot revert ownership to the deleted user")
	}

	if srv, ok := ServerShared.Get(serverID); ok {
		require.NotEqual(t, fromUser, srv.GetUserID(),
			"server owner must not be reverted to the deleted FromUserID; got owner=%d", srv.GetUserID())
	}

	if _, err := c.Cancel(tr.ID); err == nil {
		var refreshed model.ServerTransfer
		if err := DB.First(&refreshed, tr.ID).Error; err == nil {
			require.NotEqual(t, model.ServerTransferStatusPending, refreshed.Status,
				"after OnUserDelete a subsequent Cancel must not leave the transfer Pending")
			if srv, ok := ServerShared.Get(serverID); ok {
				require.NotEqual(t, fromUser, srv.GetUserID(),
					"a late Cancel against the terminated transfer must not revert server.UserID to the deleted FromUserID; got owner=%d", srv.GetUserID())
			}
		}
	}
}
