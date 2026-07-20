package evidence

import (
	"testing"
	"time"
)

func TestEvidence_TransferHashRejectsEverySuccessOnlyField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*transferEvidence)
	}{
		{"upload bytes", func(value *transferEvidence) { value.UploadBytes = 1 }},
		{"download bytes", func(value *transferEvidence) { value.DownloadBytes = 1 }},
		{"upload hash", func(value *transferEvidence) { value.UploadSHA256 = "stale" }},
		{"download hash", func(value *transferEvidence) { value.DownloadSHA256 = "stale" }},
		{"upload chunks", func(value *transferEvidence) { value.UploadChunks = 1 }},
		{"download chunks", func(value *transferEvidence) { value.DownloadChunks = 1 }},
		{"upload duration", func(value *transferEvidence) { value.UploadDuration = time.Nanosecond }},
		{"download duration", func(value *transferEvidence) { value.DownloadDuration = time.Nanosecond }},
		{"retained heap", func(value *transferEvidence) { value.RetainedHeapBytes = 1 }},
		{"mode", func(value *transferEvidence) { value.Mode = "0640" }},
		{"create dirs", func(value *transferEvidence) { value.CreateDirs = true }},
		{"upload replay", func(value *transferEvidence) { value.UploadReplayRejected = true }},
		{"download replay", func(value *transferEvidence) { value.DownloadReplayRejected = true }},
		{"oversize", func(value *transferEvidence) { value.OversizeRejected = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDedicatedExecutableEvidence(t, dir, "transfer-100mib", "transfer-hash", false)
			artifact := validTransferArtifact("transfer-hash", false)
			test.mutate(&artifact.Evidence)
			writeJSONEvidenceFile(t, dir, "transfer.json", artifact)
			if err := ValidateDirectory(dir); err == nil {
				t.Fatal("stale transfer success field accepted")
			}
		})
	}
}

func TestEvidence_DashboardExitRejectsEveryPostFaultField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*reconnectEvidence)
	}{
		{"dashboard after", func(value *reconnectEvidence) { value.Runtime.DashboardAfter.PID = 1 }},
		{"agent after", func(value *reconnectEvidence) { value.Runtime.AgentAfter.PID = 1 }},
		{"state before restart", func(value *reconnectEvidence) { value.Runtime.StateGenerationBeforeAgentRestart = 1 }},
		{"state after restart", func(value *reconnectEvidence) { value.Runtime.StateGenerationAfterAgentRestart = 1 }},
		{"identity dashboard config", func(value *reconnectEvidence) { value.Identity.DashboardConfigUnchanged = true }},
		{"identity agent config", func(value *reconnectEvidence) { value.Identity.AgentConfigUnchanged = true }},
		{"identity fixture", func(value *reconnectEvidence) { value.Identity.DashboardFixtureUnchanged = true }},
		{"clients recreated", func(value *reconnectEvidence) { value.Identity.ClientsRecreated = true }},
		{"bootstrap recreated", func(value *reconnectEvidence) { value.Identity.BootstrapRecreated = true }},
		{"reconnect timestamp", func(value *reconnectEvidence) { value.Lifecycle.ReconnectAt = time.Now() }},
		{"reconnect interval", func(value *reconnectEvidence) { value.Lifecycle.ReconnectInterval = time.Second }},
		{"dashboard receipts", func(value *reconnectEvidence) { value.Lifecycle.DashboardReceipts = []receiptPair{{}} }},
		{"agent receipts", func(value *reconnectEvidence) { value.Lifecycle.AgentReceipts = []receiptPair{{}} }},
		{"stale accounting", func(value *reconnectEvidence) { value.Lifecycle.StaleGenerationReceipts = 1 }},
		{"duplicate accounting", func(value *reconnectEvidence) { value.Lifecycle.DuplicateTaskIDs = 1 }},
		{"lost accounting", func(value *reconnectEvidence) { value.Lifecycle.LostResultIDs = 1 }},
		{"observation server", func(value *reconnectEvidence) { value.Observation.ServerID = 1 }},
		{"observation uuid", func(value *reconnectEvidence) { value.Observation.UUID = "stale" }},
		{"old generation", func(value *reconnectEvidence) { value.Observation.OldGeneration = 1 }},
		{"new generation", func(value *reconnectEvidence) { value.Observation.NewGeneration = 1 }},
		{"disconnect observation", func(value *reconnectEvidence) { value.Observation.DisconnectAt = time.Now() }},
		{"reconnect observation", func(value *reconnectEvidence) { value.Observation.ReconnectAt = time.Now() }},
		{"task ids", func(value *reconnectEvidence) { value.Observation.TaskIDs = []uint64{1} }},
		{"result ids", func(value *reconnectEvidence) { value.Observation.ResultIDs = []uint64{1} }},
		{"post reconnect", func(value *reconnectEvidence) { value.Observation.PostReconnect = true }},
		{"agent restarted", func(value *reconnectEvidence) { value.Observation.AgentRestarted = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDedicatedExecutableEvidence(t, dir, "reconnect", "dashboard-exit", false)
			artifact := validReconnectArtifact("dashboard-exit", false)
			test.mutate(&artifact.Evidence)
			writeJSONEvidenceFile(t, dir, "reconnect.json", artifact)
			if err := ValidateDirectory(dir); err == nil {
				t.Fatal("stale reconnect success field accepted")
			}
		})
	}
}
