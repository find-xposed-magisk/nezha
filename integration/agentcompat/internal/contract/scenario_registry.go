package contract

import (
	"errors"
	"slices"
)

const (
	ScenarioMetadata               = "metadata"
	ScenarioRegistrationConfigExec = "registration-config-exec"
	ScenarioNAT                    = "nat"
	ScenarioLegacyFM               = "legacy-fm"
	ScenarioTerminal               = "terminal"
	ScenarioMCPFilesystem          = "mcp-filesystem"
	ScenarioTransfer100MiB         = "transfer-100mib"
	ScenarioReconnect              = "reconnect"

	FaultAgentBadSecret = "agent-bad-secret"
	FaultTransferHash   = "transfer-hash"
	FaultDashboardExit  = "dashboard-exit"
)

type DedicatedArtifactKind uint8

const (
	DedicatedArtifactNone DedicatedArtifactKind = iota
	DedicatedArtifactTransfer
	DedicatedArtifactReconnect
)

type ScenarioExecutionKind uint8

const (
	ScenarioExecutionMetadata ScenarioExecutionKind = iota
	ScenarioExecutionRegistrationConfigExec
	ScenarioExecutionNAT
	ScenarioExecutionLegacyFM
	ScenarioExecutionTerminal
	ScenarioExecutionMCPFilesystem
	ScenarioExecutionTransfer
	ScenarioExecutionReconnect
)

type ScenarioDefinition struct {
	Name              string
	AllowedFaults     []string
	Execution         ScenarioExecutionKind
	DedicatedArtifact DedicatedArtifactKind
}

type AssertionDefinition struct {
	Name   string
	Passed bool
}

const (
	AssertionInjectedFault = "injected fault produced the expected scenario failure"

	AssertionTransferWarmup           = "small real upload and download warm-up precedes event and deadline quiescence"
	AssertionTransferUpload           = "exact 100MiB upload has size mode SHA and create_dirs"
	AssertionTransferDownload         = "exact 100MiB download has equal nonempty SHA"
	AssertionTransferUploadReplay     = "upload token replay is typed unauthorized"
	AssertionTransferDownloadReplay   = "download token replay is typed unauthorized"
	AssertionTransferOversize         = "100MiB plus one upload is typed too large"
	AssertionTransferResidue          = "Dashboard spool and Agent temp residue are zero"
	AssertionTransferSentinels        = "outside-root sentinels remain unchanged"
	AssertionTransferHeap             = "retained live heap stays within 16MiB"
	AssertionTransferCleanup          = "process listener and workspace cleanup completed"
	AssertionTransferHashRejected     = "transfer-hash rejects upload with typed 502"
	AssertionTransferHashTargetAbsent = "transfer-hash leaves target absent"

	AssertionReconnectDisconnect = "Dashboard disconnect barrier stopped generation one"
	AssertionReconnectDashboard  = "Dashboard generation two preserves fixture and recreates runtime clients"
	AssertionReconnectIdentity   = "Agent reconnect preserves exact server ID and UUID"
	AssertionReconnectReceipts   = "post-reconnect MCP task and result receipts are exactly once"
	AssertionReconnectStale      = "stale Dashboard generation cannot receive new task receipts"
	AssertionReconnectAgent      = "Agent restart advances state stream and preserves config identity"
	AssertionReconnectSentinel   = "outside-root sentinel remains unchanged"
	AssertionReconnectCleanup    = "multi-generation process listener and workspace cleanup completed"
)

func (definition ScenarioDefinition) DedicatedArtifactName() string {
	switch definition.DedicatedArtifact {
	case DedicatedArtifactNone:
		return ""
	case DedicatedArtifactTransfer:
		return "transfer.json"
	case DedicatedArtifactReconnect:
		return "reconnect.json"
	default:
		return ""
	}
}

func (definition ScenarioDefinition) Assertions(fault string) []AssertionDefinition {
	switch definition.Name {
	case ScenarioTransfer100MiB:
		if fault == FaultTransferHash {
			return assertions(
				AssertionTransferWarmup, AssertionTransferHashRejected, AssertionTransferHashTargetAbsent,
				AssertionTransferResidue, AssertionTransferSentinels, AssertionTransferCleanup, AssertionInjectedFault,
			)
		}
		return assertions(
			AssertionTransferWarmup, AssertionTransferUpload, AssertionTransferDownload,
			AssertionTransferUploadReplay, AssertionTransferDownloadReplay, AssertionTransferOversize,
			AssertionTransferResidue, AssertionTransferSentinels, AssertionTransferHeap, AssertionTransferCleanup,
		)
	case ScenarioReconnect:
		if fault == FaultDashboardExit {
			return assertions(AssertionReconnectDisconnect, AssertionReconnectSentinel, AssertionReconnectCleanup, AssertionInjectedFault)
		}
		return assertions(
			AssertionReconnectDisconnect, AssertionReconnectDashboard, AssertionReconnectIdentity,
			AssertionReconnectReceipts, AssertionReconnectStale, AssertionReconnectAgent,
			AssertionReconnectSentinel, AssertionReconnectCleanup,
		)
	default:
		return nil
	}
}

func assertions(names ...string) []AssertionDefinition {
	definitions := make([]AssertionDefinition, 0, len(names))
	for _, name := range names {
		definitions = append(definitions, AssertionDefinition{Name: name, Passed: name != AssertionInjectedFault})
	}
	return definitions
}

var scenarioDefinitions = []ScenarioDefinition{
	{Name: ScenarioMetadata, AllowedFaults: []string{""}, Execution: ScenarioExecutionMetadata},
	{Name: ScenarioRegistrationConfigExec, AllowedFaults: []string{"", FaultAgentBadSecret}, Execution: ScenarioExecutionRegistrationConfigExec},
	{Name: ScenarioNAT, AllowedFaults: []string{""}, Execution: ScenarioExecutionNAT},
	{Name: ScenarioLegacyFM, AllowedFaults: []string{"", FaultAgentBadSecret}, Execution: ScenarioExecutionLegacyFM},
	{Name: ScenarioTerminal, AllowedFaults: []string{""}, Execution: ScenarioExecutionTerminal},
	{Name: ScenarioMCPFilesystem, AllowedFaults: []string{""}, Execution: ScenarioExecutionMCPFilesystem},
	{Name: ScenarioTransfer100MiB, AllowedFaults: []string{"", FaultTransferHash}, Execution: ScenarioExecutionTransfer, DedicatedArtifact: DedicatedArtifactTransfer},
	{Name: ScenarioReconnect, AllowedFaults: []string{"", FaultDashboardExit}, Execution: ScenarioExecutionReconnect, DedicatedArtifact: DedicatedArtifactReconnect},
}

func ScenarioDefinitions() []ScenarioDefinition {
	definitions := make([]ScenarioDefinition, len(scenarioDefinitions))
	copy(definitions, scenarioDefinitions)
	for index := range definitions {
		definitions[index].AllowedFaults = slices.Clone(definitions[index].AllowedFaults)
	}
	return definitions
}

func ScenarioDefinitionByName(name string) (ScenarioDefinition, error) {
	for _, definition := range scenarioDefinitions {
		if definition.Name == name {
			definition.AllowedFaults = slices.Clone(definition.AllowedFaults)
			return definition, nil
		}
	}
	return ScenarioDefinition{}, errors.New("unsupported scenario")
}

func ValidateScenarioFault(scenario Scenario, fault Fault) error {
	definition, err := ScenarioDefinitionByName(scenario.String())
	if err != nil {
		return err
	}
	if !slices.Contains(definition.AllowedFaults, fault.String()) {
		return errors.New("unsupported fault for scenario")
	}
	return nil
}

func IsSupportedScenario(name string) bool {
	_, err := ScenarioDefinitionByName(name)
	return err == nil
}
