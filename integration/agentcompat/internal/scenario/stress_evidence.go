//go:build linux

package scenario

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

var (
	ErrStressEvidence = errors.New("stress evidence is invalid")
	ErrStressFault    = errors.New("stress worker fault evidence is invalid")
)

type StressPreparedBinaries struct {
	DashboardBuildCount int  `json:"dashboard_build_count"`
	DashboardPathReused bool `json:"dashboard_path_reused"`
	AgentBuildCount     int  `json:"agent_build_count"`
	AgentPathReused     bool `json:"agent_path_reused"`
}

type StressQuotaBoundary struct {
	Allowed         int  `json:"allowed"`
	Rejected        int  `json:"rejected"`
	AllowedAccepted bool `json:"allowed_accepted"`
	RejectedDenied  bool `json:"rejected_denied"`
}

type StressQuotaEvidence struct {
	PATSecond       StressQuotaBoundary `json:"pat_second"`
	PATMinute       StressQuotaBoundary `json:"pat_minute"`
	UserStreams     StressQuotaBoundary `json:"user_streams"`
	ServerStreams   StressQuotaBoundary `json:"server_streams"`
	PathLockStripes int                 `json:"path_lock_stripes"`
}

type StressWarmupEvidence struct {
	Agent      StressAgentOrdinal `json:"agent"`
	Exec       bool               `json:"exec"`
	Filesystem bool               `json:"filesystem"`
	Terminal   bool               `json:"terminal"`
	NAT        bool               `json:"nat"`
	FM         bool               `json:"file_manager"`
}

type StressSessionEvidence struct {
	ID        StressSessionID   `json:"id"`
	Kind      StressSessionKind `json:"kind"`
	Succeeded bool              `json:"succeeded"`
}

type StressFaultTarget struct {
	Iteration int                 `json:"iteration"`
	Round     int                 `json:"round"`
	Agent     StressAgentOrdinal  `json:"agent"`
	Kind      StressOperationKind `json:"kind"`
}

type StressCleanupSummary struct {
	Passed              bool `json:"passed"`
	ReceiptCount        int  `json:"receipt_count"`
	FailedReceiptCount  int  `json:"failed_receipt_count"`
	ForcedCleanupCount  int  `json:"forced_cleanup_count"`
	ProcessResidue      int  `json:"process_residue"`
	ProcessGroupResidue int  `json:"process_group_residue"`
	WorkspaceResidue    int  `json:"workspace_residue"`
}

type StressIterationEvidence struct {
	Iteration int                    `json:"iteration"`
	Rounds    []StressRoundEvidence  `json:"rounds"`
	Resources []StressProcessWindows `json:"resources"`
}

type StressEvidence struct {
	Version          int                       `json:"version"`
	Profile          contract.ProfileName      `json:"profile"`
	Seed             contract.Seed             `json:"seed"`
	PreparedBinaries StressPreparedBinaries    `json:"prepared_binaries"`
	Quotas           StressQuotaEvidence       `json:"quotas"`
	Warmups          []StressWarmupEvidence    `json:"warmups"`
	Sessions         []StressSessionEvidence   `json:"sessions"`
	Plan             StressPlan                `json:"plan"`
	Iterations       []StressIterationEvidence `json:"iterations"`
	FaultTarget      *StressFaultTarget        `json:"fault_target,omitempty"`
	SoakTrend        StressSoakTrendEvidence   `json:"soak_trend,omitempty"`
	Cleanup          StressCleanupSummary      `json:"cleanup"`
}

func StressWorkerFaultTarget() StressFaultTarget {
	agent, err := NewStressAgentOrdinal(4)
	if err != nil {
		panic(err)
	}
	return StressFaultTarget{Iteration: 1, Round: 2, Agent: agent, Kind: StressOperationExec}
}

func (e StressEvidence) ValidateSuccess(profile contract.Profile) error {
	return e.validate(profile, false)
}

func (e StressEvidence) ValidateStressWorker(profile contract.Profile) error {
	return e.validate(profile, true)
}

func (e StressEvidence) validate(profile contract.Profile, faultAware bool) error {
	if e.Version != 1 || e.Profile != profile.Name() || e.Seed != contract.DefaultSeed {
		return ErrStressEvidence
	}
	canonical, err := GenerateStressPlan(profile, e.Seed)
	if err != nil || e.Plan.Profile != e.Profile || e.Plan.Seed != e.Seed || !reflect.DeepEqual(e.Plan, canonical) {
		return fmt.Errorf("plan does not match canonical plan: %w", ErrStressEvidence)
	}
	if err := validateStressPreparedBinaries(e.PreparedBinaries); err != nil {
		return err
	}
	if err := e.Quotas.Validate(); err != nil {
		return err
	}
	if err := validateStressWarmups(e.Warmups, profile.AgentCount()); err != nil {
		return err
	}
	if err := validateStressSessions(e.Sessions, canonical.Sessions, profile.ConcurrentSessions()); err != nil {
		return err
	}
	if len(e.Iterations) != profile.Iterations() {
		return fmt.Errorf("iterations=%d want=%d: %w", len(e.Iterations), profile.Iterations(), ErrStressEvidence)
	}
	failed := 0
	for index, iteration := range e.Iterations {
		count, err := validateStressIteration(canonical, iteration, index+1, faultAware)
		if err != nil {
			return err
		}
		failed += count
	}
	if err := validateStressFault(e.FaultTarget, failed, faultAware); err != nil {
		return err
	}
	if profile.Iterations() == 3 {
		if err := ValidateStressSoakTrendForProfile(profile, e.SoakTrend); err != nil {
			return err
		}
	}
	if !e.Cleanup.Passed || e.Cleanup.ReceiptCount != 9 || e.Cleanup.FailedReceiptCount != 0 || e.Cleanup.ForcedCleanupCount != 0 || e.Cleanup.ProcessResidue != 0 || e.Cleanup.ProcessGroupResidue != 0 || e.Cleanup.WorkspaceResidue != 0 {
		return fmt.Errorf("cleanup=%+v: %w", e.Cleanup, ErrStressEvidence)
	}
	return nil
}

func (q StressQuotaEvidence) Validate() error {
	wants := []struct {
		got    StressQuotaBoundary
		allow  int
		reject int
	}{{q.PATSecond, 10, 11}, {q.PATMinute, 120, 121}, {q.UserStreams, 20, 21}, {q.ServerStreams, 40, 41}}
	for _, want := range wants {
		if want.got.Allowed != want.allow || want.got.Rejected != want.reject || !want.got.AllowedAccepted || !want.got.RejectedDenied {
			return fmt.Errorf("quota=%+v want=%d/%d: %w", want.got, want.allow, want.reject, ErrStressEvidence)
		}
	}
	if q.PathLockStripes != 1024 {
		return fmt.Errorf("path lock stripes=%d: %w", q.PathLockStripes, ErrStressEvidence)
	}
	return nil
}
