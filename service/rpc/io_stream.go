package rpc

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nezhahq/nezha/service/singleton"
)

type ioStreamContext struct {
	creatorUserID    uint64
	targetServerID   uint64
	userIo           io.ReadWriteCloser
	agentIo          io.ReadWriteCloser
	userIoConnectCh  chan struct{}
	agentIoConnectCh chan struct{}
	userIoChOnce     sync.Once
	agentIoChOnce    sync.Once
}

type bp struct {
	buf []byte
}

var bufPool = sync.Pool{
	New: func() any {
		return &bp{
			buf: make([]byte, 1024*1024),
		}
	},
}

func (s *NezhaHandler) CreateStream(streamId string, creatorUserID uint64, targetServerID uint64) {
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()

	s.ioStreams[streamId] = &ioStreamContext{
		creatorUserID:    creatorUserID,
		targetServerID:   targetServerID,
		userIoConnectCh:  make(chan struct{}),
		agentIoConnectCh: make(chan struct{}),
	}
}

// IsStreamAuthorizedForAgent reports whether the connecting agent is the
// server the dashboard selected when CreateStream was called. Without this
// check any authenticated agent that learns an active streamId — via
// task-stream observation, leaked logs, or a shared global agent secret —
// can race in via IOStream() and serve a terminal / fm / NAT session that
// was addressed to a different server, turning the channel into a
// session-hijack RCE primitive. This is the agent-side dual of
// IsStreamAuthorizedForUser.
func (s *NezhaHandler) IsStreamAuthorizedForAgent(streamId string, agentServerID uint64) bool {
	s.ioStreamMutex.RLock()
	defer s.ioStreamMutex.RUnlock()

	ctx, ok := s.ioStreams[streamId]
	if !ok {
		return false
	}
	return ctx.targetServerID != 0 && ctx.targetServerID == agentServerID
}

// IsStreamAuthorizedForUser checks whether the requesting user may attach to
// the stream. A stream is reachable only by its creator or by an admin; any
// other authenticated user must be rejected. Unknown streams are always
// rejected.
func (s *NezhaHandler) IsStreamAuthorizedForUser(streamId string, userID uint64, isAdmin bool) bool {
	creator, found := s.StreamOwnership(streamId)
	if !found {
		return false
	}
	if isAdmin {
		return true
	}
	return creator == userID
}

// isValidIOStreamMagic reports whether the first four bytes of an IOStream
// init message carry the ff05ff05 marker. Previously this was inlined as
// `byte0 != 0xff && byte1 != 0x05 && byte2 != 0xff && byte3 == 0x05` to
// detect *invalid* payloads — but && short-circuited so any payload whose
// byte0 happened to be 0xff slipped through. Centralising the check here and
// stating the contract positively (all four bytes must match) eliminates the
// short-circuit class of mistakes.
func isValidIOStreamMagic(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return data[0] == 0xff && data[1] == 0x05 && data[2] == 0xff && data[3] == 0x05
}

// StreamOwnership returns the user ID that created the stream and whether the
// stream is still tracked. Callers must compare the returned creator against
// the requesting user before attaching to the stream — without this the
// channel becomes a session-hijack primitive (terminal/file manager RCE).
func (s *NezhaHandler) StreamOwnership(streamId string) (uint64, bool) {
	s.ioStreamMutex.RLock()
	defer s.ioStreamMutex.RUnlock()

	ctx, ok := s.ioStreams[streamId]
	if !ok {
		return 0, false
	}
	return ctx.creatorUserID, true
}

func (s *NezhaHandler) GetStream(streamId string) (*ioStreamContext, error) {
	s.ioStreamMutex.RLock()
	defer s.ioStreamMutex.RUnlock()

	if ctx, ok := s.ioStreams[streamId]; ok {
		return ctx, nil
	}

	return nil, errors.New("stream not found")
}

// RevokeStreamsForServer tears down every IOStream whose targetServerID
// matches serverID. Called by the singleton package via the
// ServerTransferStreamRevocationHook on every transfer ownership
// transition — a stream the previous owner had open against this server
// must not survive into the new tenant, otherwise terminal/file-manager/NAT
// sessions become post-transfer hijack channels (effectively RCE).
//
// Underlying IO pipes are closed inline so the dashboard websocket loop
// sees EOF immediately rather than at the next idle-timeout.
func (s *NezhaHandler) RevokeStreamsForServer(serverID uint64) {
	if serverID == 0 {
		return
	}
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	for streamId, ctx := range s.ioStreams {
		if ctx.targetServerID != serverID {
			continue
		}
		if ctx.userIo != nil {
			ctx.userIo.Close()
		}
		if ctx.agentIo != nil {
			ctx.agentIo.Close()
		}
		delete(s.ioStreams, streamId)
	}
}

func (s *NezhaHandler) CloseStream(streamId string) error {
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()

	if ctx, ok := s.ioStreams[streamId]; ok {
		if ctx.userIo != nil {
			ctx.userIo.Close()
		}
		if ctx.agentIo != nil {
			ctx.agentIo.Close()
		}
		delete(s.ioStreams, streamId)
	}

	return nil
}



func (s *NezhaHandler) UserConnected(streamId string, userIo io.ReadWriteCloser) error {
	stream, err := s.GetStream(streamId)
	if err != nil {
		return err
	}

	stream.userIo = userIo
	stream.userIoChOnce.Do(func() {
		close(stream.userIoConnectCh)
	})

	return nil
}

func (s *NezhaHandler) AgentConnected(streamId string, agentIo io.ReadWriteCloser) error {
	stream, err := s.GetStream(streamId)
	if err != nil {
		return err
	}

	stream.agentIo = agentIo
	stream.agentIoChOnce.Do(func() {
		close(stream.agentIoConnectCh)
	})

	return nil
}

func (s *NezhaHandler) StartStream(streamId string, timeout time.Duration) error {
	stream, err := s.GetStream(streamId)
	if err != nil {
		return err
	}

	timeoutTimer := time.NewTimer(timeout)

LOOP:
	for {
		select {
		case <-stream.userIoConnectCh:
			if stream.agentIo != nil {
				timeoutTimer.Stop()
				break LOOP
			}
		case <-stream.agentIoConnectCh:
			if stream.userIo != nil {
				timeoutTimer.Stop()
				break LOOP
			}
		case <-time.After(timeout):
			break LOOP
		}
		time.Sleep(time.Millisecond * 500)
	}

	if stream.userIo == nil && stream.agentIo == nil {
		return singleton.Localizer.ErrorT("timeout: no connection established")
	}
	if stream.userIo == nil {
		return singleton.Localizer.ErrorT("timeout: user connection not established")
	}
	if stream.agentIo == nil {
		return singleton.Localizer.ErrorT("timeout: agent connection not established")
	}

	isDone := new(atomic.Bool)
	endCh := make(chan struct{})

	go func() {
		bp := bufPool.Get().(*bp)
		defer bufPool.Put(bp)
		_, innerErr := io.CopyBuffer(stream.userIo, stream.agentIo, bp.buf)
		if innerErr != nil {
			err = innerErr
		}
		if isDone.CompareAndSwap(false, true) {
			close(endCh)
		}
	}()
	go func() {
		bp := bufPool.Get().(*bp)
		defer bufPool.Put(bp)
		_, innerErr := io.CopyBuffer(stream.agentIo, stream.userIo, bp.buf)
		if innerErr != nil {
			err = innerErr
		}
		if isDone.CompareAndSwap(false, true) {
			close(endCh)
		}
	}()

	<-endCh
	return err
}
