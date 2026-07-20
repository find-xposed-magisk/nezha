//go:build agentcompat

package rpc

import (
	"crypto/rand"
)

type agentCompatCapabilityPhase uint8

const (
	agentCompatCapabilityRegistered agentCompatCapabilityPhase = iota + 1
	agentCompatCapabilityConsumed
	agentCompatCapabilityPublished
)

const (
	agentCompatCapabilityMaxActivePerPAT = 16
	agentCompatCapabilityMaxActiveGlobal = 128
	agentCompatCapabilityMaxProcessMints = 4096
)

type agentCompatCapabilityRegistration struct {
	registration AgentCompatCapabilityRegistration
	phase        agentCompatCapabilityPhase
	generation   uint64
	streamID     string
	stream       *ioStreamContext
	notify       chan struct{}
}

type agentCompatCapabilityState struct {
	active      map[string]*agentCompatCapabilityRegistration
	activeByPAT map[uint64]uint16
	// Used tokens are process-lifetime tombstones; deletion never makes a capability reusable.
	used            map[string]struct{}
	tokenSource     func([]byte) error
	nextIdentity    uint64
	waitObserver    func()
	publishObserver func()
}

func (s *NezhaHandler) initializeAgentCompatCapabilities() {
	s.agentCompatCapabilities.active = make(map[string]*agentCompatCapabilityRegistration)
	s.agentCompatCapabilities.activeByPAT = make(map[uint64]uint16)
	s.agentCompatCapabilities.used = make(map[string]struct{})
	s.agentCompatCapabilities.tokenSource = func(destination []byte) error {
		_, err := rand.Read(destination)
		return err
	}
}

func (s *NezhaHandler) setAgentCompatCapabilityTokenSourceForTest(source func([]byte) error) {
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	s.agentCompatCapabilities.tokenSource = source
}

func (s *NezhaHandler) setAgentCompatCapabilityWaitObserverForTest(observer func()) {
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	s.agentCompatCapabilities.waitObserver = observer
}

func (s *NezhaHandler) setAgentCompatCapabilityPublishObserverForTest(observer func()) {
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	s.agentCompatCapabilities.publishObserver = observer
}

func (s *NezhaHandler) SetAgentCompatCapabilityPublishObserverForTest(observer func()) {
	s.setAgentCompatCapabilityPublishObserverForTest(observer)
}

func (registration *agentCompatCapabilityRegistration) publishLocked() {
	close(registration.notify)
	registration.notify = make(chan struct{})
}
