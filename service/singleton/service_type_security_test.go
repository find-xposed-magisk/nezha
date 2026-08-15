package singleton

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

func TestServiceSentinelUpdateRejectsNonProbeTaskTypes(t *testing.T) {
	ss := &ServiceSentinel{}
	require.Error(t, ss.Update(nil))
	for _, taskType := range []uint8{0, model.TaskTypeCommand, model.TaskTypeApplyConfig, model.TaskTypeExec, 255} {
		require.Error(t, ss.Update(&model.Service{Type: taskType}), "type %d must not be scheduled", taskType)
	}
}

func TestServiceSentinelQuarantinesInvalidPersistedTypes(t *testing.T) {
	ss := newServiceMonitorSecurityHarness(t)

	insert := `INSERT INTO services
		(id, user_id, name, type, target, duration, cover, skip_servers_raw, fail_trigger_tasks_raw, recover_trigger_tasks_raw)
		VALUES (?, 100, ?, ?, 'example.invalid:443', 3600, ?, '{}', '[]', '[]')`
	require.NoError(t, DB.Exec(insert, 91, "legacy-command", model.TaskTypeCommand, model.ServiceCoverIgnoreAll).Error)
	require.NoError(t, DB.Exec(insert, 92, "legacy-apply-config", model.TaskTypeApplyConfig, model.ServiceCoverIgnoreAll).Error)
	require.NoError(t, DB.Exec(insert, 93, "valid-probe", model.TaskTypeTCPPing, model.ServiceCoverIgnoreAll).Error)

	require.NoError(t, ss.loadServiceHistory())
	_, commandLoaded := ss.Get(91)
	_, applyConfigLoaded := ss.Get(92)
	valid, validLoaded := ss.Get(93)
	require.False(t, commandLoaded)
	require.False(t, applyConfigLoaded)
	require.True(t, validLoaded)
	require.Equal(t, uint8(model.TaskTypeTCPPing), valid.Type)
}
