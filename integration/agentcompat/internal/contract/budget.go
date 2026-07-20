package contract

import (
	"errors"
	"time"
)

const (
	ResourceWarmupRuns                = 1
	ResourceSampleCount               = 5
	ResourceSampleInterval            = 250 * time.Millisecond
	ResourceExpectedCountDrift        = 0
	DashboardRSSDeltaBytes     uint64 = 64 * 1024 * 1024
	AgentRSSDeltaBytes         uint64 = 32 * 1024 * 1024
	TransferHeapBytes          uint64 = 16 * 1024 * 1024
)

type ResourceBudgetInput struct {
	WarmupRuns             int
	SampleCount            int
	SampleInterval         time.Duration
	ChildProcessCountDrift int
	ListenerCountDrift     int
	NonStdioFDCountDrift   int
	DashboardRSSDeltaBytes uint64
	AgentRSSDeltaBytes     uint64
	TransferHeapBytes      uint64
}

type ResourceBudget struct{ input ResourceBudgetInput }

func NewResourceBudget(input ResourceBudgetInput) (ResourceBudget, error) {
	if input.WarmupRuns < 1 || input.SampleCount < 1 || input.SampleInterval <= 0 || input.ChildProcessCountDrift < 0 || input.ListenerCountDrift < 0 || input.NonStdioFDCountDrift < 0 || input.DashboardRSSDeltaBytes == 0 || input.AgentRSSDeltaBytes == 0 || input.TransferHeapBytes == 0 {
		return ResourceBudget{}, errors.New("invalid resource budget")
	}
	return ResourceBudget{input: input}, nil
}

func (b ResourceBudget) WarmupRuns() int                { return b.input.WarmupRuns }
func (b ResourceBudget) SampleCount() int               { return b.input.SampleCount }
func (b ResourceBudget) SampleInterval() time.Duration  { return b.input.SampleInterval }
func (b ResourceBudget) ChildProcessCountDrift() int    { return b.input.ChildProcessCountDrift }
func (b ResourceBudget) ListenerCountDrift() int        { return b.input.ListenerCountDrift }
func (b ResourceBudget) NonStdioFDCountDrift() int      { return b.input.NonStdioFDCountDrift }
func (b ResourceBudget) DashboardRSSDeltaBytes() uint64 { return b.input.DashboardRSSDeltaBytes }
func (b ResourceBudget) AgentRSSDeltaBytes() uint64     { return b.input.AgentRSSDeltaBytes }
func (b ResourceBudget) TransferHeapBytes() uint64      { return b.input.TransferHeapBytes }

func DefaultResourceBudget() ResourceBudget {
	return ResourceBudget{input: ResourceBudgetInput{WarmupRuns: ResourceWarmupRuns, SampleCount: ResourceSampleCount, SampleInterval: ResourceSampleInterval, ChildProcessCountDrift: ResourceExpectedCountDrift, ListenerCountDrift: ResourceExpectedCountDrift, NonStdioFDCountDrift: ResourceExpectedCountDrift, DashboardRSSDeltaBytes: DashboardRSSDeltaBytes, AgentRSSDeltaBytes: AgentRSSDeltaBytes, TransferHeapBytes: TransferHeapBytes}}
}
