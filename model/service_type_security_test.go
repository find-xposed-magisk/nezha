package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceMonitorTypeAllowlist(t *testing.T) {
	for _, taskType := range []uint64{TaskTypeHTTPGet, TaskTypeICMPPing, TaskTypeTCPPing} {
		require.True(t, IsServiceMonitorType(taskType), "probe type %d must remain allowed", taskType)
		require.NoError(t, ValidateServiceMonitorType(taskType))
		require.True(t, IsServiceSentinelNeeded(taskType))
	}

	for _, taskType := range []uint64{
		0,
		TaskTypeCommand,
		TaskTypeApplyConfig,
		TaskTypeServerTransferApply,
		TaskTypeExec,
		TaskTypeFsTransfer,
		255,
	} {
		require.False(t, IsServiceMonitorType(taskType), "privileged/unknown type %d must be rejected", taskType)
		require.Error(t, ValidateServiceMonitorType(taskType))
		require.False(t, IsServiceSentinelNeeded(taskType))
	}
}

func TestServicePersistenceAndPBRejectPrivilegedTaskTypes(t *testing.T) {
	for _, taskType := range []uint8{0, TaskTypeCommand, TaskTypeApplyConfig, TaskTypeExec, 255} {
		service := &Service{Type: taskType}
		require.Error(t, service.BeforeSave(nil), "type %d must not be persisted", taskType)
		require.Nil(t, service.PB(), "type %d must not become an Agent task", taskType)
	}

	service := &Service{Common: Common{ID: 7}, Type: TaskTypeTCPPing, Target: "example.invalid:443"}
	require.NoError(t, service.BeforeSave(nil))
	task := service.PB()
	require.NotNil(t, task)
	require.Equal(t, uint64(7), task.GetId())
	require.Equal(t, uint64(TaskTypeTCPPing), task.GetType())
	require.Equal(t, service.Target, task.GetData())
}
