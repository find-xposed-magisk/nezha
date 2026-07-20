package rpc

import (
	"errors"
	"io"
)

var ErrAgentStreamAlreadyConnected = errors.New("agent stream already connected")

func (s *NezhaHandler) IsStreamAuthorizedForAgent(streamId string, agentServerID uint64) bool {
	s.ioStreamMutex.RLock()
	defer s.ioStreamMutex.RUnlock()
	ctx, ok := s.ioStreams[streamId]
	return ok && ctx.targetServerID != 0 && ctx.targetServerID == agentServerID
}

func (s *NezhaHandler) IsStreamAuthorizedForUser(streamId string, userID uint64, isAdmin bool) bool {
	creator, found := s.StreamOwnership(streamId)
	return found && (isAdmin || creator == userID)
}

func (s *NezhaHandler) StreamOwnership(streamId string) (uint64, bool) {
	s.ioStreamMutex.RLock()
	defer s.ioStreamMutex.RUnlock()
	ctx, ok := s.ioStreams[streamId]
	if !ok {
		return 0, false
	}
	return ctx.creatorUserID, true
}

func (s *NezhaHandler) StreamTarget(streamId string) (uint64, bool) {
	s.ioStreamMutex.RLock()
	defer s.ioStreamMutex.RUnlock()
	ctx, ok := s.ioStreams[streamId]
	if !ok {
		return 0, false
	}
	return ctx.targetServerID, true
}

func (s *NezhaHandler) GetStream(streamId string) (*ioStreamContext, error) {
	s.ioStreamMutex.RLock()
	defer s.ioStreamMutex.RUnlock()
	if ctx, ok := s.ioStreams[streamId]; ok {
		return ctx, nil
	}
	return nil, errors.New("stream not found")
}

func (s *NezhaHandler) UserConnected(streamId string, userIo io.ReadWriteCloser) error {
	s.ioStreamMutex.Lock()
	stream, ok := s.ioStreams[streamId]
	if !ok {
		s.ioStreamMutex.Unlock()
		return errors.New("stream not found")
	}
	stream.userIo = userIo
	s.ioStreamMutex.Unlock()
	stream.userIoChOnce.Do(func() { close(stream.userIoConnectCh) })
	return nil
}

func (s *NezhaHandler) AgentConnected(streamId string, agentIo io.ReadWriteCloser) error {
	s.ioStreamMutex.Lock()
	stream, ok := s.ioStreams[streamId]
	if !ok {
		s.ioStreamMutex.Unlock()
		return errors.Join(errors.New("stream not found"), agentIo.Close())
	}
	if stream.agentIo != nil {
		s.ioStreamMutex.Unlock()
		return errors.Join(ErrAgentStreamAlreadyConnected, agentIo.Close())
	}
	stream.agentIo = agentIo
	s.ioStreamMutex.Unlock()
	stream.agentIoChOnce.Do(func() { close(stream.agentIoConnectCh) })
	return nil
}

func (s *NezhaHandler) streamEndpoints(stream *ioStreamContext) (io.ReadWriteCloser, io.ReadWriteCloser) {
	s.ioStreamMutex.RLock()
	defer s.ioStreamMutex.RUnlock()
	return stream.userIo, stream.agentIo
}
