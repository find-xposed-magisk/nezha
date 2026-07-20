//go:build agentcompat

package rpc

import (
	"errors"
	"time"
)

func (s *NezhaHandler) CreateAgentCompatNATStream(handle AgentCompatNATPublishHandle, streamID string) (*AgentCompatNATStreamLease, error) {
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	registration := handle.registration
	if registration == nil || registration.generation != handle.generation {
		return nil, ErrAgentCompatCapabilityHidden
	}
	currentRegistration, active := s.agentCompatCapabilities.active[handle.capability]
	stored := registration.registration
	if !active || currentRegistration != registration || registration.phase != agentCompatCapabilityConsumed ||
		stored.Purpose != AgentCompatCapabilityNAT || registration.stream != nil || streamID == "" {
		return nil, ErrAgentCompatCapabilityHidden
	}
	if err := s.createStreamLocked(streamID, 0, stored.TargetServerID, PurposeNAT); err != nil {
		if err == ErrStreamAlreadyExists {
			return nil, ErrAgentCompatCapabilityHidden
		}
		return nil, err
	}
	stream := s.ioStreams[streamID]
	registration.streamID = streamID
	registration.stream = stream
	return &AgentCompatNATStreamLease{streamID: streamID, stream: stream}, nil
}

func (s *NezhaHandler) CloseAgentCompatNATStreamLease(lease *AgentCompatNATStreamLease) error {
	if lease == nil {
		return nil
	}
	return s.detachExactStream(lease.streamID, lease.stream)
}

func (s *NezhaHandler) StartAgentCompatNATStream(handle AgentCompatNATPublishHandle, timeout time.Duration) (bool, error) {
	s.ioStreamMutex.RLock()
	registration := handle.registration
	publicationOwned := registration != nil && registration.generation == handle.generation &&
		registration.phase == agentCompatCapabilityPublished && registration.streamID != "" && registration.stream != nil
	if registration == nil || registration.generation != handle.generation {
		s.ioStreamMutex.RUnlock()
		return publicationOwned, ErrAgentCompatCapabilityHidden
	}
	current, active := s.agentCompatCapabilities.active[handle.capability]
	stored := registration.registration
	streamID := registration.streamID
	stream := registration.stream
	valid := active && current == registration && registration.phase == agentCompatCapabilityPublished &&
		streamID != "" && stream != nil && s.ioStreams[streamID] == stream &&
		stream.creatorUserID == 0 && stream.targetServerID == stored.TargetServerID &&
		stream.purpose == PurposeNAT && stored.Purpose == AgentCompatCapabilityNAT
	s.ioStreamMutex.RUnlock()
	if !valid {
		return publicationOwned, ErrAgentCompatCapabilityHidden
	}
	startErr := s.startStreamContext(streamID, stream, timeout)
	closeErr := s.detachExactStream(streamID, stream)
	return publicationOwned, errors.Join(startErr, closeErr)
}
