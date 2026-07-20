//go:build linux && agentcompat

package scenario

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func validHeldSessionSetRealEvidenceFromPlan() heldSessionSetRealEvidence {
	profile, _ := contract.ProfileByName(string(contract.ProfilePRFull))
	plan, _ := GenerateStressPlan(profile, contract.DefaultSeed)
	value := heldSessionSetRealEvidence{Version: 1, Profile: string(contract.ProfilePRFull), Seed: "4e5a4841", BaselineCount: 1, LiveCount: 13, ClosedCount: 1, TerminalCount: 4, NATCount: 4, FMCount: 4, AgentOrdinals: []int{1, 2, 3, 4, 5, 6, 7, 8}, ProtocolProved: true, ExactIDsPresent: true, ExactIDsAbsent: true, PIDStable: true, ResourcesAbsent: true, ProcessesClean: true, WorkspacesClean: true, CleanupOK: true}
	for index := 1; index <= 8; index++ {
		value.AgentSummaries = append(value.AgentSummaries, heldSessionSetRealAgentSummary{Ordinal: index, ServerDigest: heldRealDigest(string(rune('a' + index))), PATIdentity: true, PATScopeExact: true})
	}
	for index, session := range plan.Sessions {
		digest := heldRealDigest(string(rune('A' + index)))
		value.SessionDigests = append(value.SessionDigests, digest)
		value.SessionSummaries = append(value.SessionSummaries, heldSessionSetRealSessionSummary{Ordinal: index + 1, Kind: string(session.Kind), AgentOrdinal: session.Agent.Int(), StreamDigest: digest, Present: true, Absent: true, Protocol: true})
	}
	return value
}

func TestHeldSessionSetRealPlanUsesCanonicalPRFullTopology(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)
	require.Equal(t, "pr-full", string(plan.Profile))
	require.Equal(t, uint64(0x4e5a4841), uint64(plan.Seed))
	require.Len(t, plan.Sessions, 12)
	require.Equal(t, 4, countHeldSessionKind(plan.Sessions, StressSessionTerminal))
	require.Equal(t, 4, countHeldSessionKind(plan.Sessions, StressSessionNAT))
	require.Equal(t, 4, countHeldSessionKind(plan.Sessions, StressSessionFM))
	for ordinal := 1; ordinal <= heldSessionSetAgentCount; ordinal++ {
		require.Contains(t, realHeldSessionSetAgentOrdinals(plan), ordinal)
	}
}

func TestHeldSessionSetRealEvidenceRejectsIncompleteAndRedactsArtifact(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	evidence := validHeldSessionSetRealEvidence()
	require.Error(t, validateHeldSessionSetRealEvidence(heldSessionSetRealEvidence{}))
	require.NoError(t, validateHeldSessionSetRealEvidence(evidence))
	require.NoError(t, writeHeldSessionSetRealEvidence(root, evidence))
	readBack, err := readHeldSessionSetRealEvidence(root)
	require.NoError(t, err)
	require.Equal(t, evidence, readBack)
}

func TestHeldSessionSetRealEvidenceRejectsCanonicalMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*heldSessionSetRealEvidence)
	}{
		{name: "version zero", mutate: func(value *heldSessionSetRealEvidence) { value.Version = 0 }},
		{name: "wrong version", mutate: func(value *heldSessionSetRealEvidence) { value.Version = 2 }},
		{name: "wrong seed", mutate: func(value *heldSessionSetRealEvidence) { value.Seed = "4e5a4842" }},
		{name: "uppercase digest", mutate: func(value *heldSessionSetRealEvidence) {
			value.SessionDigests[0] = strings.ToUpper(value.SessionDigests[0])
		}},
		{name: "non hex digest", mutate: func(value *heldSessionSetRealEvidence) { value.SessionDigests[0] = strings.Repeat("z", 64) }},
		{name: "duplicate server digest", mutate: func(value *heldSessionSetRealEvidence) {
			value.AgentSummaries[1].ServerDigest = value.AgentSummaries[0].ServerDigest
		}},
		{name: "duplicate session digest", mutate: func(value *heldSessionSetRealEvidence) {
			value.SessionDigests[1] = value.SessionDigests[0]
			value.SessionSummaries[1].StreamDigest = value.SessionDigests[0]
		}},
		{name: "summary mismatch", mutate: func(value *heldSessionSetRealEvidence) {
			value.SessionSummaries[0].StreamDigest = heldRealDigest("different")
		}},
		{name: "digest order", mutate: func(value *heldSessionSetRealEvidence) { slices.Reverse(value.SessionDigests) }},
		{name: "malformed length", mutate: func(value *heldSessionSetRealEvidence) { value.SessionDigests[0] = value.SessionDigests[0][:63] }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			value := validHeldSessionSetRealEvidence()
			testCase.mutate(&value)
			err := validateHeldSessionSetRealEvidence(value)
			require.ErrorIs(t, err, ErrHeldSessionSetRealEvidenceInvalid)
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

func validHeldSessionSetRealEvidence() heldSessionSetRealEvidence {
	return validHeldSessionSetRealEvidenceFromPlan()
}
