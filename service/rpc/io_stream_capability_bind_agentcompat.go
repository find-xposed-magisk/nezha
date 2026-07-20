//go:build agentcompat

package rpc

import "context"

func (s *NezhaHandler) BindAgentCompatIOStreamCapability(binding AgentCompatCapabilityBinding) error {
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	registration, allowed := s.agentCompatRegistrationLocked(binding.AgentCompatCapabilityAccess)
	if !allowed || registration.phase != agentCompatCapabilityRegistered || registration.registration.Purpose == AgentCompatCapabilityNAT {
		return ErrAgentCompatCapabilityHidden
	}
	stream, exists := s.ioStreams[binding.StreamID]
	stored := registration.registration
	if !exists || binding.StreamID == "" || stream.creatorUserID != stored.Owner.UserID ||
		stream.targetServerID != stored.TargetServerID || stream.purpose != stored.Purpose.streamPurpose() {
		return ErrAgentCompatCapabilityHidden
	}
	if registration.stream != nil {
		if registration.stream == stream && registration.streamID == binding.StreamID {
			return nil
		}
		return ErrAgentCompatCapabilityConflict
	}
	registration.streamID = binding.StreamID
	registration.stream = stream
	registration.publishLocked()
	return nil
}

func (s *NezhaHandler) WaitAgentCompatIOStreamCapability(ctx context.Context, access AgentCompatCapabilityAccess) (string, error) {
	for {
		s.ioStreamMutex.RLock()
		registration, allowed := s.agentCompatRegistrationLocked(access)
		if !allowed {
			s.ioStreamMutex.RUnlock()
			return "", ErrAgentCompatCapabilityHidden
		}
		if registration.streamID != "" {
			streamID := registration.streamID
			stream := registration.stream
			stored := registration.registration
			current, live := s.ioStreams[streamID]
			if stored.Purpose == AgentCompatCapabilityNAT && registration.phase == agentCompatCapabilityPublished && stream != nil {
				s.ioStreamMutex.RUnlock()
				return streamID, nil
			}
			// A reused StreamID must not turn a retained capability into authority over a replacement stream.
			creatorMatches := stream != nil && stream.creatorUserID == stored.Owner.UserID
			if stored.Purpose == AgentCompatCapabilityNAT {
				creatorMatches = stream != nil && stream.creatorUserID == 0
			}
			valid := live && current == stream && creatorMatches &&
				stream.targetServerID == stored.TargetServerID && stream.purpose == stored.Purpose.streamPurpose()
			s.ioStreamMutex.RUnlock()
			if !valid {
				return "", ErrAgentCompatCapabilityHidden
			}
			return streamID, nil
		}
		notify := registration.notify
		observer := s.agentCompatCapabilities.waitObserver
		s.ioStreamMutex.RUnlock()
		if observer != nil {
			observer()
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-notify:
		}
	}
}
