//go:build agentcompat

package rpc

func (s *NezhaHandler) ConsumeAgentCompatNATCapability(access AgentCompatCapabilityAccess) (AgentCompatNATPublishHandle, error) {
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	registration, allowed := s.agentCompatRegistrationLocked(access)
	if !allowed {
		return AgentCompatNATPublishHandle{}, ErrAgentCompatCapabilityHidden
	}
	return s.consumeAgentCompatNATCapabilityLocked(registration, access.Capability.value)
}

func (s *NezhaHandler) ConsumeAgentCompatNATCapabilityForProfile(value string, targetServerID, resourceID uint64) (AgentCompatCapabilityAccess, AgentCompatNATPublishHandle, error) {
	capability, err := ParseAgentCompatIOStreamCapability(value)
	if err != nil {
		return AgentCompatCapabilityAccess{}, AgentCompatNATPublishHandle{}, ErrAgentCompatCapabilityHidden
	}

	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	registration, exists := s.agentCompatCapabilities.active[capability.value]
	if !exists || registration.registration.Purpose != AgentCompatCapabilityNAT ||
		registration.registration.TargetServerID != targetServerID || registration.registration.ResourceID != resourceID {
		return AgentCompatCapabilityAccess{}, AgentCompatNATPublishHandle{}, ErrAgentCompatCapabilityHidden
	}
	handle, err := s.consumeAgentCompatNATCapabilityLocked(registration, capability.value)
	if err != nil {
		return AgentCompatCapabilityAccess{}, AgentCompatNATPublishHandle{}, err
	}
	stored := registration.registration
	return AgentCompatCapabilityAccess{
		Capability: capability, Owner: stored.Owner, Purpose: stored.Purpose,
		TargetServerID: stored.TargetServerID, ResourceID: stored.ResourceID,
		ServerAccessAllowed: stored.ServerAccessAllowed,
	}, handle, nil
}

func (s *NezhaHandler) consumeAgentCompatNATCapabilityLocked(registration *agentCompatCapabilityRegistration, capability string) (AgentCompatNATPublishHandle, error) {
	if registration == nil || registration.registration.Purpose != AgentCompatCapabilityNAT || registration.phase != agentCompatCapabilityRegistered {
		return AgentCompatNATPublishHandle{}, ErrAgentCompatCapabilityHidden
	}
	registration.phase = agentCompatCapabilityConsumed
	return AgentCompatNATPublishHandle{
		registration: registration, generation: registration.generation,
		capability: capability,
	}, nil
}

func (s *NezhaHandler) PublishAgentCompatNATStream(handle AgentCompatNATPublishHandle, publication AgentCompatNATPublication) error {
	s.ioStreamMutex.RLock()
	publishObserver := s.agentCompatCapabilities.publishObserver
	s.ioStreamMutex.RUnlock()
	if publishObserver != nil {
		publishObserver()
	}
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	registration := handle.registration
	// Pointer identity plus generation makes a late publisher inert after unregister/cancel.
	if registration == nil || registration.generation != handle.generation {
		return nil
	}
	current, active := s.agentCompatCapabilities.active[handle.capability]
	if !active || current != registration {
		return nil
	}
	if registration.phase == agentCompatCapabilityPublished {
		return nil
	}
	stored := registration.registration
	stream := registration.stream
	exists := publication.StreamID != "" && registration.streamID == publication.StreamID && stream != nil && s.ioStreams[publication.StreamID] == stream
	if registration.phase != agentCompatCapabilityConsumed || publication.Purpose != stored.Purpose ||
		publication.TargetServerID != stored.TargetServerID || publication.ResourceID != stored.ResourceID ||
		!exists || stream.creatorUserID != 0 ||
		stream.targetServerID != stored.TargetServerID || stream.purpose != PurposeNAT {
		return ErrAgentCompatCapabilityHidden
	}
	registration.phase = agentCompatCapabilityPublished
	registration.publishLocked()
	return nil
}
