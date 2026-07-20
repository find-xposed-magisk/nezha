package contract

import (
	"slices"
	"testing"
)

func TestContract_ScenarioFaultRegistry(t *testing.T) {
	tests := []struct {
		scenario string
		fault    string
		valid    bool
	}{
		{ScenarioTransfer100MiB, "", true},
		{ScenarioTransfer100MiB, FaultTransferHash, true},
		{ScenarioTransfer100MiB, FaultDashboardExit, false},
		{ScenarioReconnect, "", true},
		{ScenarioReconnect, FaultDashboardExit, true},
		{ScenarioReconnect, FaultTransferHash, false},
		{ScenarioRegistrationConfigExec, FaultAgentBadSecret, true},
		{ScenarioLegacyFM, FaultAgentBadSecret, true},
		{ScenarioMCPFilesystem, FaultAgentBadSecret, false},
		{"future-scenario", "", false},
	}
	for _, test := range tests {
		scenario, err := NewScenario(test.scenario)
		if err != nil {
			t.Fatalf("construct scenario %q: %v", test.scenario, err)
		}
		fault := Fault{}
		if test.fault != "" {
			fault, err = NewFault(test.fault)
			if err != nil {
				t.Fatalf("construct fault %q: %v", test.fault, err)
			}
		}
		err = ValidateScenarioFault(scenario, fault)
		if (err == nil) != test.valid {
			t.Fatalf("scenario=%q fault=%q valid=%t err=%v", test.scenario, test.fault, test.valid, err)
		}
	}
}

func TestContract_SyntacticConstructorsPreserveArbitraryValidNames(t *testing.T) {
	if _, err := NewScenario("future-scenario"); err != nil {
		t.Fatalf("valid future scenario syntax rejected: %v", err)
	}
	if _, err := NewFault("future-fault"); err != nil {
		t.Fatalf("valid future fault syntax rejected: %v", err)
	}
}

func TestContract_ScenarioDefinitionsAreDeterministicAndTyped(t *testing.T) {
	definitions := ScenarioDefinitions()
	wantNames := []string{
		ScenarioMetadata,
		ScenarioRegistrationConfigExec,
		ScenarioNAT,
		ScenarioLegacyFM,
		ScenarioTerminal,
		ScenarioMCPFilesystem,
		ScenarioTransfer100MiB,
		ScenarioReconnect,
	}
	gotNames := make([]string, 0, len(definitions))
	seenExecution := make(map[ScenarioExecutionKind]struct{}, len(definitions))
	for _, definition := range definitions {
		gotNames = append(gotNames, definition.Name)
		if len(definition.AllowedFaults) == 0 || definition.AllowedFaults[0] != "" {
			t.Fatalf("scenario %q does not explicitly allow the no-fault path", definition.Name)
		}
		if _, exists := seenExecution[definition.Execution]; exists {
			t.Fatalf("execution kind is duplicated: %d", definition.Execution)
		}
		seenExecution[definition.Execution] = struct{}{}
		if definition.DedicatedArtifactName() == "" && definition.DedicatedArtifact != DedicatedArtifactNone {
			t.Fatalf("scenario %q has unnamed dedicated artifact", definition.Name)
		}
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("scenario enumeration order=%v want=%v", gotNames, wantNames)
	}
}
