//go:build linux && agentcompat

package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

var ErrHeldSessionSetRealEvidenceInvalid = errors.New("held session set evidence is invalid")

type heldSessionSetRealEvidence struct {
	Version          int                                `json:"version"`
	Profile          string                             `json:"profile"`
	Seed             string                             `json:"seed"`
	BaselineCount    int                                `json:"baseline_count"`
	LiveCount        int                                `json:"live_count"`
	ClosedCount      int                                `json:"closed_count"`
	TerminalCount    int                                `json:"terminal_count"`
	NATCount         int                                `json:"nat_count"`
	FMCount          int                                `json:"fm_count"`
	AgentOrdinals    []int                              `json:"agent_ordinals"`
	AgentSummaries   []heldSessionSetRealAgentSummary   `json:"agent_summaries"`
	SessionSummaries []heldSessionSetRealSessionSummary `json:"session_summaries"`
	SessionDigests   []string                           `json:"session_digests"`
	ProtocolProved   bool                               `json:"protocol_proved"`
	ExactIDsPresent  bool                               `json:"exact_ids_present"`
	ExactIDsAbsent   bool                               `json:"exact_ids_absent"`
	PIDStable        bool                               `json:"pid_stable"`
	ResourcesAbsent  bool                               `json:"resources_absent"`
	ProcessesClean   bool                               `json:"processes_clean"`
	WorkspacesClean  bool                               `json:"workspaces_clean"`
	CleanupOK        bool                               `json:"cleanup_ok"`
}

type heldSessionSetRealAgentSummary struct {
	Ordinal       int    `json:"ordinal"`
	ServerDigest  string `json:"server_digest"`
	PATIdentity   bool   `json:"pat_identity"`
	PATScopeExact bool   `json:"pat_scope_exact"`
}

type heldSessionSetRealSessionSummary struct {
	Ordinal      int    `json:"ordinal"`
	Kind         string `json:"kind"`
	AgentOrdinal int    `json:"agent_ordinal"`
	StreamDigest string `json:"stream_digest"`
	Present      bool   `json:"present"`
	Absent       bool   `json:"absent"`
	Protocol     bool   `json:"protocol"`
}

func validateHeldSessionSetRealEvidence(evidenceValue heldSessionSetRealEvidence) error {
	if evidenceValue.Version != 1 {
		return ErrHeldSessionSetRealEvidenceInvalid
	}
	if evidenceValue.Profile != string(contract.ProfilePRFull) || evidenceValue.Seed != "4e5a4841" || evidenceValue.BaselineCount < 0 || evidenceValue.LiveCount != evidenceValue.BaselineCount+12 || evidenceValue.ClosedCount != evidenceValue.BaselineCount {
		return ErrHeldSessionSetRealEvidenceInvalid
	}
	if evidenceValue.TerminalCount != 4 || evidenceValue.NATCount != 4 || evidenceValue.FMCount != 4 || !slices.Equal(evidenceValue.AgentOrdinals, []int{1, 2, 3, 4, 5, 6, 7, 8}) || len(evidenceValue.AgentSummaries) != 8 || len(evidenceValue.SessionSummaries) != 12 || len(evidenceValue.SessionDigests) != 12 {
		return ErrHeldSessionSetRealEvidenceInvalid
	}
	serverDigests := make(map[string]struct{}, len(evidenceValue.AgentSummaries))
	for index, summary := range evidenceValue.AgentSummaries {
		if summary.Ordinal != index+1 || !validHeldSessionSetRealDigest(summary.ServerDigest) || !summary.PATIdentity || !summary.PATScopeExact {
			return ErrHeldSessionSetRealEvidenceInvalid
		}
		if _, exists := serverDigests[summary.ServerDigest]; exists {
			return ErrHeldSessionSetRealEvidenceInvalid
		}
		serverDigests[summary.ServerDigest] = struct{}{}
	}
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	if err != nil {
		return ErrHeldSessionSetRealEvidenceInvalid
	}
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	if err != nil || len(plan.Sessions) != 12 {
		return ErrHeldSessionSetRealEvidenceInvalid
	}
	sessionDigests := make(map[string]struct{}, len(evidenceValue.SessionDigests))
	for index, digest := range evidenceValue.SessionDigests {
		if !validHeldSessionSetRealDigest(digest) {
			return ErrHeldSessionSetRealEvidenceInvalid
		}
		if _, exists := sessionDigests[digest]; exists {
			return ErrHeldSessionSetRealEvidenceInvalid
		}
		sessionDigests[digest] = struct{}{}
		if digest != evidenceValue.SessionSummaries[index].StreamDigest {
			return ErrHeldSessionSetRealEvidenceInvalid
		}
	}
	kindCounts := map[StressSessionKind]int{}
	for index, summary := range evidenceValue.SessionSummaries {
		canonical := plan.Sessions[index]
		if summary.Ordinal != index+1 || summary.Kind != string(canonical.Kind) || summary.AgentOrdinal != canonical.Agent.Int() || !validHeldSessionSetRealDigest(summary.StreamDigest) || !summary.Present || !summary.Absent || !summary.Protocol {
			return ErrHeldSessionSetRealEvidenceInvalid
		}
		kindCounts[canonical.Kind]++
	}
	if kindCounts[StressSessionTerminal] != 4 || kindCounts[StressSessionNAT] != 4 || kindCounts[StressSessionFM] != 4 {
		return ErrHeldSessionSetRealEvidenceInvalid
	}
	if !evidenceValue.ProtocolProved || !evidenceValue.ExactIDsPresent || !evidenceValue.ExactIDsAbsent || !evidenceValue.PIDStable || !evidenceValue.ResourcesAbsent || !evidenceValue.ProcessesClean || !evidenceValue.WorkspacesClean || !evidenceValue.CleanupOK {
		return ErrHeldSessionSetRealEvidenceInvalid
	}
	return nil
}

func validHeldSessionSetRealDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func writeHeldSessionSetRealEvidence(root string, evidenceValue heldSessionSetRealEvidence) error {
	if err := validateHeldSessionSetRealEvidence(evidenceValue); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(root)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("held session set evidence root is not a private directory")
	}
	path := filepath.Join(root, "held-session-set.json")
	if stale, err := os.Lstat(path); err == nil {
		if stale.Mode()&os.ModeSymlink != 0 || !stale.Mode().IsRegular() {
			return errors.New("stale held session set evidence is not a regular file")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(evidenceValue)
	if err != nil {
		return err
	}
	if evidence.Redact(string(data)) != string(data) {
		return errors.New("held session set evidence requires redaction")
	}
	temporary, err := os.CreateTemp(root, ".held-session-set-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func readHeldSessionSetRealEvidence(root string) (heldSessionSetRealEvidence, error) {
	var result heldSessionSetRealEvidence
	data, err := os.ReadFile(filepath.Join(root, "held-session-set.json"))
	if err != nil {
		return result, fmt.Errorf("read held session set evidence: %w", err)
	}
	if evidence.Redact(string(data)) != string(data) {
		return result, errors.New("held session set evidence is not redacted")
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	return result, validateHeldSessionSetRealEvidence(result)
}
