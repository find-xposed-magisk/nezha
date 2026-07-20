package contract

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type ProfileName string
type Seed uint64

const (
	ProfilePRFull ProfileName = "pr-full"
	ProfileSoak   ProfileName = "soak"
	DefaultSeed   Seed        = 0x4e5a4841

	PRFullJobTimeout           = 75 * time.Minute
	PRFullSuiteDeadline        = 55 * time.Minute
	PRFullAgentCount           = 8
	PRFullStressRounds         = 4
	PRFullConcurrentOperations = 64
	PRFullConcurrentSessions   = 4
	PRFullTransferPairs        = 1
	PRFullRestartCycles        = 1

	SoakJobTimeout           = 150 * time.Minute
	SoakSuiteDeadline        = 150 * time.Minute
	SoakAgentCount           = 20
	SoakStressRounds         = 4
	SoakConcurrentOperations = 160
	SoakConcurrentSessions   = 4
	SoakTransferPairs        = 5
	SoakRestartCycles        = 10
	SoakIterations           = 3

	TransferBytes          uint64 = 100 * 1024 * 1024
	StreamBoundaryAllowed         = 40
	StreamBoundaryRejected        = 41
)

type Profile struct {
	name                   ProfileName
	jobTimeout             time.Duration
	suiteDeadline          time.Duration
	seed                   Seed
	agentCount             int
	stressRounds           int
	concurrentOperations   int
	concurrentSessions     int
	transferPairs          int
	dashboardRestartCycles int
	iterations             int
	streamBoundaryAllowed  int
	streamBoundaryRejected int
}

func ProfileByName(name string) (Profile, error) {
	switch ProfileName(name) {
	case ProfilePRFull:
		return Profile{name: ProfilePRFull, jobTimeout: PRFullJobTimeout, suiteDeadline: PRFullSuiteDeadline, seed: DefaultSeed, agentCount: PRFullAgentCount, stressRounds: PRFullStressRounds, concurrentOperations: PRFullConcurrentOperations, concurrentSessions: PRFullConcurrentSessions, transferPairs: PRFullTransferPairs, dashboardRestartCycles: PRFullRestartCycles, iterations: 1, streamBoundaryAllowed: StreamBoundaryAllowed, streamBoundaryRejected: StreamBoundaryRejected}, nil
	case ProfileSoak:
		return Profile{name: ProfileSoak, jobTimeout: SoakJobTimeout, suiteDeadline: SoakSuiteDeadline, seed: DefaultSeed, agentCount: SoakAgentCount, stressRounds: SoakStressRounds, concurrentOperations: SoakConcurrentOperations, concurrentSessions: SoakConcurrentSessions, transferPairs: SoakTransferPairs, dashboardRestartCycles: SoakRestartCycles, iterations: SoakIterations, streamBoundaryAllowed: StreamBoundaryAllowed, streamBoundaryRejected: StreamBoundaryRejected}, nil
	default:
		return Profile{}, errors.New("unknown profile; expected pr-full or soak")
	}
}

func (p Profile) Name() ProfileName            { return p.name }
func (p Profile) JobTimeout() time.Duration    { return p.jobTimeout }
func (p Profile) SuiteDeadline() time.Duration { return p.suiteDeadline }
func (p Profile) Seed() Seed                   { return p.seed }
func (p Profile) AgentCount() int              { return p.agentCount }
func (p Profile) StressRounds() int            { return p.stressRounds }
func (p Profile) ConcurrentOperations() int    { return p.concurrentOperations }
func (p Profile) ConcurrentSessions() int      { return p.concurrentSessions }
func (p Profile) TransferPairs() int           { return p.transferPairs }
func (p Profile) DashboardRestartCycles() int  { return p.dashboardRestartCycles }
func (p Profile) Iterations() int              { return p.iterations }
func (p Profile) TransferBytes() uint64        { return TransferBytes }
func (p Profile) StreamBoundaryAllowed() int   { return p.streamBoundaryAllowed }
func (p Profile) StreamBoundaryRejected() int  { return p.streamBoundaryRejected }
func (p Profile) StreamBoundaryCheck() bool {
	return p.streamBoundaryAllowed > 0 && p.streamBoundaryRejected > p.streamBoundaryAllowed
}

var ErrInvalidSeed = errors.New("invalid seed")

func ParseSeed(raw string) (Seed, error) {
	value, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(raw, "0x"), "0X"), 16, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%w; expected nonzero hexadecimal", ErrInvalidSeed)
	}
	return Seed(value), nil
}
