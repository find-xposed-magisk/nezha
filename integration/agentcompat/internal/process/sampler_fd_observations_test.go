//go:build linux

package process

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestSampleProcessWithFDObservations_CapturesEverySuccessfulNonStdioReadlink(t *testing.T) {
	// Given / When
	sample, err := SampleProcessWithFDObservations(os.Getpid())

	// Then
	requireNoError(t, err)
	if len(sample.FDObservations) != sample.NonStdioFDCount {
		t.Fatalf("FD observations = %d, non-stdio FD count = %d", len(sample.FDObservations), sample.NonStdioFDCount)
	}
	if sample.SampledAt.IsZero() {
		t.Fatal("sampled at is zero")
	}
	encoded, err := json.Marshal(sample)
	requireNoError(t, err)
	var evidence map[string]json.RawMessage
	requireNoError(t, json.Unmarshal(encoded, &evidence))
	if _, exists := evidence["sampled_at"]; exists {
		t.Fatalf("sampled_at leaked into evidence JSON: %s", encoded)
	}
}

func TestSampleWindowWithFDObservations_RecordsCompletionTimeForEverySample(t *testing.T) {
	// Given / When
	window, err := SampleWindow(context.Background(), WindowSpec{PID: os.Getpid(), Interval: time.Nanosecond, CaptureFDObservations: true})

	// Then
	requireNoError(t, err)
	for index, sample := range window.Samples {
		if sample.SampledAt.IsZero() {
			t.Fatalf("sample %d sampled at is zero", index+1)
		}
	}
}
