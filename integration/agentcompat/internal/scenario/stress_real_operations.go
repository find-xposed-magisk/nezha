//go:build linux && agentcompat

package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

type stressOperationExecutor struct {
	fixture *heldSessionSetRealFixture
	plan    StressOperationPlan
}

func (executor stressOperationExecutor) run(ctx context.Context) StressOperationEvidence {
	started := time.Now()
	proof, err := executor.execute(ctx)
	completed := time.Now()
	evidenceValue := StressOperationEvidence{ID: executor.plan.ID, Round: executor.plan.Round, Agent: executor.plan.Agent, PAT: executor.plan.PAT, Kind: executor.plan.Kind, LaunchedAt: started, CompletedAt: completed, Succeeded: err == nil, SuccessProof: proof}
	if err != nil {
		evidenceValue.Error = errorText(err)
	}
	return evidenceValue
}

func (executor stressOperationExecutor) execute(ctx context.Context) (string, error) {
	if executor.fixture == nil || executor.plan.Agent.Int() < 1 || executor.plan.Agent.Int() > len(executor.fixture.agents) || executor.plan.Agent.Int() > len(executor.fixture.readiness) || executor.plan.Agent.Int() > len(executor.fixture.agentPATs) {
		return "", errors.New("stress operation fixture mapping is invalid")
	}
	if executor.plan.PAT.String() == "" {
		return "", errors.New("stress operation PAT is empty")
	}
	serverID := executor.fixture.readiness[executor.plan.Agent.Int()-1].ServerID
	patIdentity, err := stressOperationPATIdentity(executor.fixture, executor.plan)
	if err != nil {
		return "", err
	}
	switch executor.plan.Kind {
	case StressOperationExec:
		return executor.exec(ctx, patIdentity.Client, serverID)
	case StressOperationFilesystem:
		return executor.filesystem(ctx, patIdentity.Client, serverID)
	default:
		return "", errors.New("unsupported stress operation kind")
	}
}

func stressOperationPATIdentity(fixture *heldSessionSetRealFixture, plan StressOperationPlan) (heldRealPATIdentity, error) {
	index := plan.Agent.Int() - 1
	for _, round := range fixture.plan.Rounds {
		for _, planned := range round.Operations {
			if planned.ID == plan.ID {
				if planned.Agent != plan.Agent || planned.Kind != plan.Kind || planned.PAT != plan.PAT {
					return heldRealPATIdentity{}, errors.New("stress operation plan PAT mapping is invalid")
				}
				return fixture.agentPATs[index], nil
			}
		}
	}
	for _, round := range fixture.plan.Rounds {
		for _, planned := range round.Operations {
			if planned.Agent == plan.Agent && planned.PAT == plan.PAT && planned.Kind == plan.Kind {
				return fixture.agentPATs[index], nil
			}
		}
	}
	return heldRealPATIdentity{}, errors.New("stress operation is absent from canonical plan")
}

func runStressWarmups(ctx context.Context, fixture *heldSessionSetRealFixture, plan StressPlan) ([]StressWarmupEvidence, error) {
	warmups := make([]StressWarmupEvidence, 0, len(fixture.agents))
	for agentIndex := range fixture.agents {
		agentOrdinal, err := NewStressAgentOrdinal(agentIndex + 1)
		if err != nil {
			return nil, err
		}
		pat := StressPATID{}
		for _, operation := range plan.Rounds[0].Operations {
			if operation.Agent == agentOrdinal {
				pat = operation.PAT
				break
			}
		}
		if pat.String() == "" {
			return nil, errors.New("stress warmup PAT mapping is invalid")
		}
		execID, err := NewStressOperationID(fmt.Sprintf("warmup-exec-a%02d", agentOrdinal.Int()))
		if err != nil {
			return nil, err
		}
		execResult := stressOperationExecutor{fixture: fixture, plan: StressOperationPlan{ID: execID, Agent: agentOrdinal, PAT: pat, Kind: StressOperationExec}}
		if _, err := execResult.execute(ctx); err != nil {
			return nil, err
		}
		filesystemID, err := NewStressOperationID(fmt.Sprintf("warmup-filesystem-a%02d", agentOrdinal.Int()))
		if err != nil {
			return nil, err
		}
		filesystemResult := stressOperationExecutor{fixture: fixture, plan: StressOperationPlan{ID: filesystemID, Round: 0, Agent: agentOrdinal, PAT: pat, Kind: StressOperationFilesystem}}
		if _, err := filesystemResult.execute(ctx); err != nil {
			return nil, err
		}
		warmups = append(warmups, StressWarmupEvidence{Agent: agentOrdinal, Exec: true, Filesystem: true, Terminal: true, NAT: true, FM: true})
	}
	return warmups, nil
}

func (executor stressOperationExecutor) exec(ctx context.Context, patClient *client.Client, serverID uint64) (string, error) {
	const token = "agentcompat-stress-exec-proof"
	result, err := client.CallTool[execArguments, execResult](ctx, patClient, client.ToolCall[execArguments]{Name: "server.exec", Arguments: execArguments{ServerID: serverID, Cmd: "/bin/sh", Args: []string{"-c", "printf " + token}}})
	if err != nil || result.StructuredContent.ExitCode != 0 || result.StructuredContent.Stdout != token || result.StructuredContent.Error != "" || result.StructuredContent.TimedOut || result.StructuredContent.StdoutTruncated {
		return "", errors.New("stress Exec proof failed")
	}
	return stressProof(token), nil
}

func (executor stressOperationExecutor) filesystem(ctx context.Context, patClient *client.Client, serverID uint64) (string, error) {
	parent := executor.fixture.agents[executor.plan.Agent.Int()-1].WorkspaceRoot()
	return executeStressFilesystemProof(ctx, patClient, serverID, parent, executor.plan.ID.String(), executor.plan.Round)
}

func executeStressFilesystemProof(ctx context.Context, patClient *client.Client, serverID uint64, parent, operationID string, round int) (string, error) {
	root, err := fixture.NewAgentRoot(parent, fmt.Sprintf("stress-%s", operationID))
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(root.Absolute())
	filesystem := newMCPFilesystemClient(patClient, serverID, root)
	content := "agentcompat-stress-filesystem-proof"
	relative := fmt.Sprintf("round-%d/%s.txt", round, operationID)
	written, err := filesystem.write(ctx, mcpFilesystemWrite{relative: relative, content: content, encoding: "utf8", mode: "0600", createDirs: true})
	if err != nil {
		return "", fmt.Errorf("stress filesystem write proof failed: %w", err)
	}
	if written.StructuredContent.Size != int64(len(content)) || written.StructuredContent.SHA256 != stressProof(content) || written.StructuredContent.Error != "" {
		return "", errors.New("stress filesystem write proof response invalid")
	}
	return stressProof(content), nil
}

func stressProof(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
