package evidence

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func validateDedicatedArtifact(files evidenceSnapshot, metadata Metadata, result ScenarioResult) error {
	definition, err := contract.ScenarioDefinitionByName(result.Name)
	if err != nil {
		return err
	}
	switch definition.DedicatedArtifact {
	case contract.DedicatedArtifactTransfer:
		artifact, err := readJSONFile[transferArtifact](files, "transfer.json")
		if err != nil {
			return err
		}
		return validateTransferArtifact(metadata, result, artifact)
	case contract.DedicatedArtifactReconnect:
		artifact, err := readJSONFile[reconnectArtifact](files, "reconnect.json")
		if err != nil {
			return err
		}
		return validateReconnectArtifact(metadata, result, artifact)
	case contract.DedicatedArtifactNone:
		return nil
	default:
		return errors.New("unsupported dedicated artifact kind")
	}
}

func validateReconnectArtifact(metadata Metadata, result ScenarioResult, artifact reconnectArtifact) error {
	if err := validateArtifactHeader(metadata, result, artifact.Scenario, artifact.Fault, artifact.Passed, artifact.CleanupOK, artifact.Error); err != nil {
		return err
	}
	switch artifact.Fault {
	case "":
		if !artifact.Passed {
			return errors.New("reconnect success artifact reports failure")
		}
		if err := validateReconnectSuccess(artifact.Evidence); err != nil {
			return err
		}
		if err := validateReconnectSuccessReceipts(artifact.Evidence); err != nil {
			return err
		}
		return validateReconnectFinalEvidence(artifact.Evidence, 2)
	case contract.FaultDashboardExit:
		if artifact.Passed || !strings.Contains(artifact.Error, "injected Dashboard exit") {
			return errors.New("dashboard-exit artifact does not identify the injected failure")
		}
		evidence := artifact.Evidence
		if reconnectPostFaultEvidencePresent(evidence) {
			return errors.New("dashboard-exit artifact presents stale reconnect success")
		}
		if evidence.Lifecycle.DisconnectAt.IsZero() || !evidence.Lifecycle.ReconnectAt.IsZero() || !evidence.Lifecycle.OutsideRootSentinelUnchanged {
			return errors.New("dashboard-exit lifecycle evidence is invalid")
		}
		return validateReconnectFinalEvidence(evidence, 1)
	default:
		return errors.New("reconnect artifact has unsupported fault")
	}
}

func reconnectPostFaultEvidencePresent(evidence reconnectEvidence) bool {
	return evidence.Runtime.DashboardAfter != (runtimeIdentity{}) || evidence.Runtime.AgentAfter != (runtimeIdentity{}) ||
		evidence.Runtime.StateGenerationBeforeAgentRestart != 0 || evidence.Runtime.StateGenerationAfterAgentRestart != 0 ||
		evidence.Identity.DashboardConfigUnchanged || evidence.Identity.AgentConfigUnchanged || evidence.Identity.DashboardFixtureUnchanged || evidence.Identity.ClientsRecreated || evidence.Identity.BootstrapRecreated ||
		!evidence.Lifecycle.ReconnectAt.IsZero() || evidence.Lifecycle.ReconnectInterval != 0 || len(evidence.Lifecycle.DashboardReceipts) != 0 || len(evidence.Lifecycle.AgentReceipts) != 0 ||
		evidence.Lifecycle.StaleGenerationReceipts != 0 || evidence.Lifecycle.DuplicateTaskIDs != 0 || evidence.Lifecycle.LostResultIDs != 0 ||
		evidence.Observation.ServerID != 0 || evidence.Observation.UUID != "" || evidence.Observation.OldGeneration != 0 || evidence.Observation.NewGeneration != 0 ||
		!evidence.Observation.DisconnectAt.IsZero() || !evidence.Observation.ReconnectAt.IsZero() || len(evidence.Observation.TaskIDs) != 0 || len(evidence.Observation.ResultIDs) != 0 || evidence.Observation.PostReconnect || evidence.Observation.AgentRestarted
}

func validateReconnectSuccess(evidence reconnectEvidence) error {
	observation := evidence.Observation
	if observation.ServerID == 0 || observation.UUID == "" || observation.OldGeneration == 0 || observation.NewGeneration <= observation.OldGeneration || observation.DisconnectAt.IsZero() || !observation.ReconnectAt.After(observation.DisconnectAt) || len(observation.TaskIDs) != 5 || !slices.Equal(observation.TaskIDs, observation.ResultIDs) || !observation.PostReconnect || !observation.AgentRestarted {
		return errors.New("reconnect observation evidence is invalid")
	}
	if evidence.Runtime.DashboardAfter.Generation <= evidence.Runtime.DashboardBefore.Generation || evidence.Runtime.DashboardAfter.PID == evidence.Runtime.DashboardBefore.PID || evidence.Runtime.AgentAfter.Generation <= evidence.Runtime.AgentBefore.Generation || evidence.Runtime.AgentAfter.PID == evidence.Runtime.AgentBefore.PID || evidence.Runtime.StateGenerationAfterAgentRestart <= evidence.Runtime.StateGenerationBeforeAgentRestart {
		return errors.New("reconnect runtime generations are invalid")
	}
	identity := evidence.Identity
	if identity.ServerID != observation.ServerID || identity.UUID != observation.UUID || !identity.DashboardConfigUnchanged || !identity.AgentConfigUnchanged || !identity.DashboardFixtureUnchanged || !identity.ClientsRecreated || !identity.BootstrapRecreated {
		return errors.New("reconnect identity evidence is invalid")
	}
	if evidence.Lifecycle.StaleGenerationReceipts != 0 || evidence.Lifecycle.DuplicateTaskIDs != 0 || evidence.Lifecycle.LostResultIDs != 0 || !evidence.Lifecycle.OutsideRootSentinelUnchanged {
		return errors.New("reconnect lifecycle accounting is invalid")
	}
	return nil
}

func validateReconnectSuccessReceipts(evidence reconnectEvidence) error {
	if len(evidence.Lifecycle.DashboardReceipts) != 3 || len(evidence.Lifecycle.AgentReceipts) != 2 || len(evidence.Observation.TaskIDs) != 5 {
		return errors.New("reconnect receipt accounting is not exact")
	}
	if evidence.Lifecycle.DisconnectAt.IsZero() || evidence.Lifecycle.ReconnectAt.IsZero() || evidence.Lifecycle.ReconnectAt.Sub(evidence.Lifecycle.DisconnectAt) != evidence.Lifecycle.ReconnectInterval {
		return errors.New("reconnect timestamp interval is inconsistent")
	}
	if !evidence.Observation.DisconnectAt.Equal(evidence.Lifecycle.DisconnectAt) || !evidence.Observation.ReconnectAt.Equal(evidence.Lifecycle.ReconnectAt) {
		return errors.New("reconnect lifecycle and observation timestamps differ")
	}
	return nil
}

func validateReconnectFinalEvidence(evidence reconnectEvidence, expectedProcessCount int) error {
	if !evidence.AgentCleanup.Passed || evidence.AgentCleanup.Forced || !evidence.DashboardCleanup.Passed || evidence.DashboardCleanup.Forced {
		return errors.New("reconnect cleanup receipts failed")
	}
	if len(evidence.AgentCleanup.Processes) != expectedProcessCount || len(evidence.DashboardCleanup.Processes) != expectedProcessCount {
		return errors.New("reconnect cleanup receipt process count is invalid")
	}
	for _, path := range []string{evidence.Fixture.Dashboard.WorkspaceRoot, evidence.Fixture.AgentRoot, evidence.Fixture.Dashboard.ConfigPath, evidence.Fixture.AgentConfigPath, evidence.Fixture.Dashboard.BinaryPath, evidence.Fixture.AgentBinaryPath} {
		if path == "" || !filepath.IsAbs(path) {
			return errors.New("reconnect fixture paths are invalid")
		}
	}
	return nil
}
