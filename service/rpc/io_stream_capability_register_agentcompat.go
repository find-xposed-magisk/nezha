//go:build agentcompat

package rpc

import (
	"context"
	"encoding/base64"
)

const agentCompatCapabilityTokenAttempts = 32

func validAgentCompatRegistration(registration AgentCompatCapabilityRegistration) bool {
	if !registration.ServerAccessAllowed || registration.Owner.PATID == 0 || registration.Owner.UserID == 0 || registration.TargetServerID == 0 {
		return false
	}
	switch registration.Purpose {
	case AgentCompatCapabilityTerminal, AgentCompatCapabilityFileManager:
		return registration.ResourceID == 0
	case AgentCompatCapabilityNAT:
		return registration.ResourceID != 0
	default:
		return false
	}
}

func (s *NezhaHandler) RegisterAgentCompatIOStreamCapability(ctx context.Context, registration AgentCompatCapabilityRegistration) (AgentCompatIOStreamCapability, error) {
	if !validAgentCompatRegistration(registration) {
		return AgentCompatIOStreamCapability{}, ErrAgentCompatCapabilityHidden
	}
	if err := ctx.Err(); err != nil {
		return AgentCompatIOStreamCapability{}, err
	}
	s.ioStreamMutex.RLock()
	tokenSource := s.agentCompatCapabilities.tokenSource
	quotaAvailable := s.agentCompatCapabilityQuotaAvailableLocked(registration.Owner.PATID)
	s.ioStreamMutex.RUnlock()
	if !quotaAvailable {
		return AgentCompatIOStreamCapability{}, ErrAgentCompatCapabilityUnavailable
	}
	for range agentCompatCapabilityTokenAttempts {
		if err := ctx.Err(); err != nil {
			return AgentCompatIOStreamCapability{}, err
		}
		// Token generation may block or reenter the registry, so it must never run under ioStreamMutex.
		raw := make([]byte, 32)
		if err := tokenSource(raw); err != nil {
			return AgentCompatIOStreamCapability{}, err
		}
		capability := AgentCompatIOStreamCapability{value: base64.RawURLEncoding.EncodeToString(raw)}

		if err := ctx.Err(); err != nil {
			return AgentCompatIOStreamCapability{}, err
		}
		s.ioStreamMutex.Lock()
		// Recheck every quota under the insertion lock so concurrent mints cannot oversubscribe any bound.
		if !s.agentCompatCapabilityQuotaAvailableLocked(registration.Owner.PATID) {
			s.ioStreamMutex.Unlock()
			return AgentCompatIOStreamCapability{}, ErrAgentCompatCapabilityUnavailable
		}
		if _, used := s.agentCompatCapabilities.used[capability.value]; used {
			s.ioStreamMutex.Unlock()
			continue
		}
		s.agentCompatCapabilities.used[capability.value] = struct{}{}
		s.agentCompatCapabilities.nextIdentity++
		s.agentCompatCapabilities.activeByPAT[registration.Owner.PATID]++
		s.agentCompatCapabilities.active[capability.value] = &agentCompatCapabilityRegistration{
			registration: registration, phase: agentCompatCapabilityRegistered,
			generation: s.agentCompatCapabilities.nextIdentity, notify: make(chan struct{}),
		}
		s.ioStreamMutex.Unlock()
		return capability, nil
	}
	return AgentCompatIOStreamCapability{}, ErrAgentCompatCapabilityTokenExhausted
}

func (s *NezhaHandler) agentCompatCapabilityQuotaAvailableLocked(patID uint64) bool {
	return s.agentCompatCapabilities.activeByPAT[patID] < agentCompatCapabilityMaxActivePerPAT &&
		len(s.agentCompatCapabilities.active) < agentCompatCapabilityMaxActiveGlobal &&
		len(s.agentCompatCapabilities.used) < agentCompatCapabilityMaxProcessMints
}

func sameAgentCompatOwner(left, right AgentCompatCapabilityOwner) bool {
	return left == right
}

func agentCompatAccessMatches(access AgentCompatCapabilityAccess, registration *agentCompatCapabilityRegistration) bool {
	stored := registration.registration
	return access.ServerAccessAllowed && sameAgentCompatOwner(access.Owner, stored.Owner) &&
		access.Purpose == stored.Purpose && access.TargetServerID == stored.TargetServerID && access.ResourceID == stored.ResourceID
}

func (s *NezhaHandler) agentCompatRegistrationLocked(access AgentCompatCapabilityAccess) (*agentCompatCapabilityRegistration, bool) {
	registration, exists := s.agentCompatCapabilities.active[access.Capability.value]
	return registration, exists && agentCompatAccessMatches(access, registration)
}
