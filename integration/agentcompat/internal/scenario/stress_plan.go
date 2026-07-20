//go:build linux

package scenario

import (
	"errors"
	"fmt"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

var ErrStressPlan = errors.New("stress plan is invalid")

type StressOperationPlan struct {
	ID    StressOperationID   `json:"id"`
	Round int                 `json:"round"`
	Agent StressAgentOrdinal  `json:"agent"`
	PAT   StressPATID         `json:"pat_id"`
	Kind  StressOperationKind `json:"kind"`
}

type StressRoundPlan struct {
	Round      int                   `json:"round"`
	Operations []StressOperationPlan `json:"operations"`
}

type StressSessionPlan struct {
	ID      StressSessionID    `json:"id"`
	Kind    StressSessionKind  `json:"kind"`
	Ordinal int                `json:"ordinal"`
	Agent   StressAgentOrdinal `json:"agent"`
}

type StressPlan struct {
	Profile  contract.ProfileName `json:"profile"`
	Seed     contract.Seed        `json:"seed"`
	Rounds   []StressRoundPlan    `json:"rounds"`
	Sessions []StressSessionPlan  `json:"sessions"`
}

func GenerateStressPlan(profile contract.Profile, seed contract.Seed) (StressPlan, error) {
	if seed == 0 || profile.AgentCount() < 1 || profile.StressRounds() < 1 || profile.ConcurrentSessions() < 1 {
		return StressPlan{}, ErrStressPlan
	}
	expectedOperations := profile.AgentCount() * profile.StressRounds() * 2
	if profile.ConcurrentOperations() != expectedOperations {
		return StressPlan{}, fmt.Errorf("concurrent operations=%d want=%d: %w", profile.ConcurrentOperations(), expectedOperations, ErrStressPlan)
	}
	plan := StressPlan{Profile: profile.Name(), Seed: seed}
	plan.Rounds = make([]StressRoundPlan, profile.StressRounds())
	for roundIndex := range plan.Rounds {
		round := roundIndex + 1
		operations, err := stressRoundOperations(seed, round, profile.AgentCount())
		if err != nil {
			return StressPlan{}, err
		}
		plan.Rounds[roundIndex] = StressRoundPlan{Round: round, Operations: operations}
	}
	sessions, err := stressSessionPlans(seed, profile.AgentCount(), profile.ConcurrentSessions())
	if err != nil {
		return StressPlan{}, err
	}
	plan.Sessions = sessions
	return plan, nil
}

func stressRoundOperations(seed contract.Seed, round, agentCount int) ([]StressOperationPlan, error) {
	operations := make([]StressOperationPlan, 0, agentCount*2)
	for agentValue := 1; agentValue <= agentCount; agentValue++ {
		agent, err := NewStressAgentOrdinal(agentValue)
		if err != nil {
			return nil, err
		}
		pat, err := NewStressPATID(fmt.Sprintf("pat-%016x-a%02d", uint64(seed), agentValue))
		if err != nil {
			return nil, err
		}
		for _, kind := range []StressOperationKind{StressOperationExec, StressOperationFilesystem} {
			identity, idErr := NewStressOperationID(fmt.Sprintf("op-%016x-r%02d-a%02d-%s", uint64(seed), round, agentValue, kind))
			if idErr != nil {
				return nil, idErr
			}
			operations = append(operations, StressOperationPlan{ID: identity, Round: round, Agent: agent, PAT: pat, Kind: kind})
		}
	}
	random := stressRandom(uint64(seed) ^ uint64(round)*0x9e3779b97f4a7c15)
	for index := len(operations) - 1; index > 0; index-- {
		swap := int(random.next() % uint64(index+1))
		operations[index], operations[swap] = operations[swap], operations[index]
	}
	return operations, nil
}

func stressSessionPlans(seed contract.Seed, agentCount, countPerKind int) ([]StressSessionPlan, error) {
	sessions := make([]StressSessionPlan, 0, countPerKind*3)
	for _, kind := range []StressSessionKind{StressSessionTerminal, StressSessionNAT, StressSessionFM} {
		for index := 1; index <= countPerKind; index++ {
			agent, err := NewStressAgentOrdinal((index-1)%agentCount + 1)
			if err != nil {
				return nil, err
			}
			identity, err := NewStressSessionID(fmt.Sprintf("session-%016x-%s-%02d", uint64(seed), kind, index))
			if err != nil {
				return nil, err
			}
			sessions = append(sessions, StressSessionPlan{ID: identity, Kind: kind, Ordinal: index, Agent: agent})
		}
	}
	return sessions, nil
}

type stressRandom uint64

func (random *stressRandom) next() uint64 {
	*random += 0x9e3779b97f4a7c15
	value := uint64(*random)
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}
