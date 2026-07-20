package singleton

import (
	"context"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"
)

const lifecycleTestTimeout = time.Second

func TestCronClassClose_waitsForRunningJobs(t *testing.T) {
	// Given
	started := make(chan struct{})
	release := make(chan struct{})
	events := make(chan string, 9)
	cronClass := &CronClass{Cron: cron.New(cron.WithSeconds())}
	_, err := cronClass.AddFunc("@every 1ns", func() {
		defer func() { events <- "job" }()
		close(started)
		<-release
	})
	require.NoError(t, err)
	cronClass.Start()
	<-started

	// When
	closed := make(chan struct{})
	for range 8 {
		go func() {
			cronClass.Close()
			events <- "close"
			closed <- struct{}{}
		}()
	}

	// Then
	close(release)
	firstEvent := awaitCronLifecycleEvent(t, events, "cron lifecycle did not complete")
	if firstEvent != "job" {
		t.Fatalf("Close returned before the running cron job returned: first event=%q", firstEvent)
	}
	for range 8 {
		awaitCronLifecycleSignal(t, closed, "concurrent Close call did not return")
	}
	for range 8 {
		if event := awaitCronLifecycleEvent(t, events, "concurrent Close call did not complete"); event != "close" {
			t.Fatalf("unexpected cron lifecycle event: %q", event)
		}
	}
}

func TestCronClassClose_isIdempotentAndNilSafe(t *testing.T) {
	cronClass := &CronClass{Cron: cron.New(cron.WithSeconds())}
	cronClass.Start()

	closed := make(chan struct{})
	for range 8 {
		go func() {
			cronClass.Close()
			closed <- struct{}{}
		}()
	}
	for range 8 {
		awaitCronLifecycleSignal(t, closed, "concurrent Close call did not return")
	}
	cronClass.Close()
	var nilCronClass *CronClass
	nilCronClass.Close()
	(&CronClass{}).Close()
}

func awaitCronLifecycleSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal(message)
	}
}

func awaitCronLifecycleEvent(t *testing.T, events <-chan string, message string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel()
	select {
	case event := <-events:
		return event
	case <-ctx.Done():
		t.Fatal(message)
		return ""
	}
}
