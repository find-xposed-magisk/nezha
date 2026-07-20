//go:build agentcompat

package rpc

import (
	"errors"
	"io"
)

func (s *NezhaHandler) CancelAgentCompatIOStreamCapability(access AgentCompatCapabilityAccess) error {
	s.ioStreamMutex.Lock()
	registration, exists := s.agentCompatCapabilities.active[access.Capability.value]
	if !exists {
		s.ioStreamMutex.Unlock()
		return nil
	}
	if !agentCompatAccessMatches(access, registration) {
		s.ioStreamMutex.Unlock()
		// Foreign and absent capabilities intentionally share the same inert result to prevent enumeration.
		return nil
	}
	if registration.stream == nil || registration.streamID == "" {
		s.removeAgentCompatCapabilityLocked(access.Capability.value, registration)
		s.ioStreamMutex.Unlock()
		return nil
	}
	stream := registration.stream
	stored := registration.registration
	current, live := s.ioStreams[registration.streamID]
	if !live {
		s.removeAgentCompatCapabilityLocked(access.Capability.value, registration)
		s.ioStreamMutex.Unlock()
		return nil
	}
	creatorMatches := stream.creatorUserID == stored.Owner.UserID
	if stored.Purpose == AgentCompatCapabilityNAT {
		creatorMatches = stream.creatorUserID == 0
	}
	if !access.ServerAccessAllowed || current != stream || !creatorMatches ||
		stream.targetServerID != stored.TargetServerID || stream.purpose != stored.Purpose.streamPurpose() {
		s.ioStreamMutex.Unlock()
		return nil
	}
	stream.revoke()
	endpoints := make([]io.ReadWriteCloser, 0, 2)
	if stream.userIo != nil {
		endpoints = append(endpoints, stream.userIo)
	}
	if stream.agentIo != nil {
		endpoints = append(endpoints, stream.agentIo)
	}
	delete(s.ioStreams, registration.streamID)
	s.publishIOStreamStateChangeLocked()
	s.removeAgentCompatCapabilityLocked(access.Capability.value, registration)
	s.ioStreamMutex.Unlock()

	closeErrors := make([]error, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if err := endpoint.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}

func (s *NezhaHandler) UnregisterAgentCompatIOStreamCapability(access AgentCompatCapabilityAccess) error {
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	registration, exists := s.agentCompatCapabilities.active[access.Capability.value]
	if !exists {
		return nil
	}
	if registration.registration.Owner.PATID != access.Owner.PATID || !agentCompatAccessMatches(access, registration) {
		return nil
	}
	if registration.stream != nil {
		if current, live := s.ioStreams[registration.streamID]; live && current == registration.stream {
			return ErrAgentCompatCapabilityBound
		}
	}
	s.removeAgentCompatCapabilityLocked(access.Capability.value, registration)
	return nil
}

func (s *NezhaHandler) removeAgentCompatCapabilityLocked(capability string, registration *agentCompatCapabilityRegistration) {
	current, active := s.agentCompatCapabilities.active[capability]
	if !active || current != registration {
		return
	}
	delete(s.agentCompatCapabilities.active, capability)
	patID := registration.registration.Owner.PATID
	remaining := s.agentCompatCapabilities.activeByPAT[patID] - 1
	if remaining == 0 {
		delete(s.agentCompatCapabilities.activeByPAT, patID)
	} else {
		s.agentCompatCapabilities.activeByPAT[patID] = remaining
	}
	registration.publishLocked()
}
