//go:build linux

package scenario

import "fmt"

func validateStressPreparedBinaries(evidence StressPreparedBinaries) error {
	if evidence.DashboardBuildCount != 1 || !evidence.DashboardPathReused || evidence.AgentBuildCount != 1 || !evidence.AgentPathReused {
		return fmt.Errorf("prepared binaries=%+v: %w", evidence, ErrStressEvidence)
	}
	return nil
}

func validateStressWarmups(warmups []StressWarmupEvidence, agentCount int) error {
	if len(warmups) != agentCount {
		return fmt.Errorf("warmups=%d want=%d: %w", len(warmups), agentCount, ErrStressEvidence)
	}
	seen := make(map[int]struct{}, len(warmups))
	for _, warmup := range warmups {
		if _, duplicate := seen[warmup.Agent.Int()]; duplicate || !warmup.Exec || !warmup.Filesystem || !warmup.Terminal || !warmup.NAT || !warmup.FM {
			return fmt.Errorf("warmup=%+v: %w", warmup, ErrStressEvidence)
		}
		seen[warmup.Agent.Int()] = struct{}{}
	}
	return nil
}

func validateStressSessions(sessions []StressSessionEvidence, plan []StressSessionPlan, countPerKind int) error {
	if len(sessions) != countPerKind*3 {
		return fmt.Errorf("sessions=%d want=%d: %w", len(sessions), countPerKind*3, ErrStressEvidence)
	}
	if len(plan) != len(sessions) {
		return fmt.Errorf("session plan=%d evidence=%d: %w", len(plan), len(sessions), ErrStressEvidence)
	}
	expected := make(map[StressSessionID]StressSessionPlan, len(plan))
	for _, session := range plan {
		expected[session.ID] = session
	}
	counts := make(map[StressSessionKind]int, 3)
	seen := make(map[StressSessionID]struct{}, len(sessions))
	for _, session := range sessions {
		planned, exists := expected[session.ID]
		_, duplicate := seen[session.ID]
		if !exists || planned.Kind != session.Kind || duplicate || !session.Succeeded {
			return fmt.Errorf("session=%+v: %w", session, ErrStressEvidence)
		}
		seen[session.ID] = struct{}{}
		counts[session.Kind]++
	}
	for _, kind := range []StressSessionKind{StressSessionTerminal, StressSessionNAT, StressSessionFM} {
		if counts[kind] != countPerKind {
			return fmt.Errorf("session kind=%s count=%d want=%d: %w", kind, counts[kind], countPerKind, ErrStressEvidence)
		}
	}
	return nil
}

func validateStressIteration(plan StressPlan, evidence StressIterationEvidence, iteration int, faultAware bool) (int, error) {
	if evidence.Iteration != iteration || len(evidence.Rounds) != len(plan.Rounds) || len(evidence.Resources) != 1+canonicalAgentCount(plan) {
		return 0, fmt.Errorf("iteration=%+v: %w", evidence, ErrStressEvidence)
	}
	failed := 0
	for index, round := range evidence.Rounds {
		matched, err := matchStressRoundEvidence(plan.Rounds[index], round)
		if err != nil {
			return 0, err
		}
		for _, operation := range matched {
			if !operation.Succeeded || operation.Error != "" {
				failed++
				if !faultAware || !isStressFaultOperation(plan.Rounds[index], operation, iteration) {
					return 0, fmt.Errorf("unexpected failed operation=%s: %w", operation.ID.String(), ErrStressFault)
				}
			}
		}
	}
	if err := validateStressResourceIdentities(plan, evidence.Resources); err != nil {
		return 0, err
	}
	for _, windows := range evidence.Resources {
		if _, err := EvaluateStressResource(windows); err != nil {
			return 0, err
		}
	}
	return failed, nil
}

func validateStressFault(target *StressFaultTarget, failed int, faultAware bool) error {
	if !faultAware {
		if target != nil || failed != 0 {
			return ErrStressFault
		}
		return nil
	}
	want := StressWorkerFaultTarget()
	if target == nil || *target != want || failed != 1 {
		return fmt.Errorf("target=%+v failed=%d want=%+v/1: %w", target, failed, want, ErrStressFault)
	}
	return nil
}

func isStressFaultOperation(plan StressRoundPlan, evidence StressOperationEvidence, iteration int) bool {
	target := StressWorkerFaultTarget()
	if iteration != target.Iteration || plan.Round != target.Round {
		return false
	}
	for _, operation := range plan.Operations {
		if operation.ID == evidence.ID {
			return operation.Agent == target.Agent && operation.Kind == target.Kind
		}
	}
	return false
}

func planAgentCount(plan StressPlan) int {
	return canonicalAgentCount(plan)
}

func canonicalAgentCount(plan StressPlan) int {
	if len(plan.Rounds) == 0 {
		return 0
	}
	return len(plan.Rounds[0].Operations) / 2
}

func validateStressResourceIdentities(plan StressPlan, resources []StressProcessWindows) error {
	wantAgents := planAgentCount(plan)
	if len(resources) != wantAgents+1 {
		return fmt.Errorf("resources=%d want=%d: %w", len(resources), wantAgents+1, ErrStressEvidence)
	}
	dashboardCount := 0
	seenAgents := make(map[int]struct{}, wantAgents)
	for _, resource := range resources {
		switch resource.Process.Kind {
		case StressProcessDashboard:
			dashboardCount++
		case StressProcessAgent:
			ordinal := resource.Process.Agent.Int()
			if ordinal < 1 || ordinal > wantAgents {
				return fmt.Errorf("unknown agent ordinal=%d: %w", ordinal, ErrStressEvidence)
			}
			if _, duplicate := seenAgents[ordinal]; duplicate {
				return fmt.Errorf("duplicate agent ordinal=%d: %w", ordinal, ErrStressEvidence)
			}
			seenAgents[ordinal] = struct{}{}
		default:
			return fmt.Errorf("unknown process kind=%q: %w", resource.Process.Kind, ErrStressEvidence)
		}
	}
	if dashboardCount != 1 || len(seenAgents) != wantAgents {
		return fmt.Errorf("dashboard=%d agents=%d want=1/%d: %w", dashboardCount, len(seenAgents), wantAgents, ErrStressEvidence)
	}
	return nil
}
