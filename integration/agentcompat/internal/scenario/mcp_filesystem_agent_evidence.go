//go:build linux

package scenario

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
)

type permissionAgentContract struct {
	filesystem      mcpFilesystemClient
	uid             uint32
	gid             uint32
	processContract bool
}

func observePermissionAgent(agentInstance *agent.Agent, credential *syscall.Credential, filesystem mcpFilesystemClient) (permissionAgentContract, error) {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", agentInstance.PID()))
	if err != nil {
		return permissionAgentContract{}, fmt.Errorf("read permission Agent process status: %w", err)
	}
	uid, err := effectiveProcessIdentity(status, "Uid:")
	if err != nil {
		return permissionAgentContract{}, err
	}
	gid, err := effectiveProcessIdentity(status, "Gid:")
	if err != nil {
		return permissionAgentContract{}, err
	}
	return permissionAgentContract{
		filesystem:      filesystem,
		uid:             uid,
		gid:             gid,
		processContract: uid == credential.Uid && gid == credential.Gid,
	}, nil
}

func effectiveProcessIdentity(status []byte, field string) (uint32, error) {
	for line := range strings.SplitSeq(string(status), "\n") {
		if !strings.HasPrefix(line, field) {
			continue
		}
		values := strings.Fields(strings.TrimPrefix(line, field))
		if len(values) < 2 {
			break
		}
		identity, err := strconv.ParseUint(values[1], 10, 32)
		if err != nil {
			return 0, fmt.Errorf("parse effective process %s: %w", field, err)
		}
		return uint32(identity), nil
	}
	return 0, fmt.Errorf("process status missing %s", field)
}
