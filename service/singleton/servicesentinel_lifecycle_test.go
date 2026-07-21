package singleton

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nezhahq/nezha/model"
)

const serviceSentinelLifecycleSuccessMarker = "service-sentinel-stale-report-lifecycle-success"

func TestServiceSentinelWorkerIgnoresStaleReportAfterDeletion(t *testing.T) {
	if os.Getenv("NEZHA_SERVICE_SENTINEL_LIFECYCLE_CHILD") == "1" {
		testServiceSentinelWorkerIgnoresStaleReportAfterDeletionChild(t)
		return
	}

	// Given
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServiceSentinelWorkerIgnoresStaleReportAfterDeletion$")
	child.Env = append(os.Environ(), "NEZHA_SERVICE_SENTINEL_LIFECYCLE_CHILD=1")

	// When
	output, err := child.CombinedOutput()

	// Then
	if ctx.Err() != nil {
		t.Fatalf("service sentinel lifecycle child timed out: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("service sentinel lifecycle child failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), serviceSentinelLifecycleSuccessMarker) {
		t.Fatalf("service sentinel lifecycle child did not report success:\n%s", output)
	}
}

func testServiceSentinelWorkerIgnoresStaleReportAfterDeletionChild(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ss := newServiceMonitorSecurityHarness(t,
		&model.Server{Common: model.Common{ID: 1, UserID: 1}, Name: "reporter"},
	)
	for _, service := range []*model.Service{
		{
			Common:      model.Common{ID: 10, UserID: 1},
			Name:        "stale-service",
			Type:        model.TaskTypeTCPPing,
			Target:      "stale.example.invalid:443",
			Duration:    3600,
			Cover:       model.ServiceCoverIgnoreAll,
			SkipServers: map[uint64]bool{1: true},
		},
		{
			Common:      model.Common{ID: 20, UserID: 1},
			Name:        "valid-service",
			Type:        model.TaskTypeTCPPing,
			Target:      "valid.example.invalid:443",
			Duration:    3600,
			Cover:       model.ServiceCoverIgnoreAll,
			SkipServers: map[uint64]bool{1: true},
		},
	} {
		addServiceMonitorSecurityService(t, ss, service)
	}
	acceptedStaleReport := make(chan struct{})
	releaseWorker := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorkerHook := func() {
		releaseOnce.Do(func() { close(releaseWorker) })
	}
	ss.serviceReportValidatedHook = func(serviceID uint64) {
		if serviceID == 10 {
			close(acceptedStaleReport)
			<-releaseWorker
		}
	}
	t.Cleanup(func() {
		releaseWorkerHook()
		ss.Close()
	})

	// When
	ss.Dispatch(serviceMonitorResult(1, 10, model.TaskTypeTCPPing, true))
	select {
	case <-acceptedStaleReport:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ss.Delete([]uint64{10})
	releaseWorkerHook()
	ss.Dispatch(serviceMonitorResult(1, 20, model.TaskTypeTCPPing, true))
	ss.Close()

	// Then
	var staleHistoryCount int64
	if err := DB.Model(&model.ServiceHistory{}).
		Where("service_id = ? AND server_id = ?", 10, 1).
		Count(&staleHistoryCount).Error; err != nil {
		t.Fatal(err)
	}
	if staleHistoryCount != 0 {
		t.Fatalf("expected stale service to write zero per-reporter history rows, got %d", staleHistoryCount)
	}
	var validHistoryCount int64
	if err := DB.Model(&model.ServiceHistory{}).
		Where("service_id = ? AND server_id = ?", 20, 1).
		Count(&validHistoryCount).Error; err != nil {
		t.Fatal(err)
	}
	if validHistoryCount != 1 {
		t.Fatalf("expected exactly one valid service history row, got %d", validHistoryCount)
	}
	ss.serviceResponseDataStoreLock.RLock()
	_, stalePingCached := ss.serviceResponsePing[10]
	validStats := ss.serviceStatusToday[20]
	ss.serviceResponseDataStoreLock.RUnlock()
	if stalePingCached {
		t.Fatal("expected stale service ping cache to be deleted")
	}
	if validStats == nil || validStats.Up != 1 || validStats.Down != 0 {
		t.Fatalf("expected valid service stats up=1 down=0, got %+v", validStats)
	}
	if _, err := fmt.Fprintln(os.Stdout, serviceSentinelLifecycleSuccessMarker); err != nil {
		t.Fatal(err)
	}
}

func TestServiceSentinelWorkerRevalidatesReportAfterUpdate(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ss := newServiceMonitorSecurityHarness(t,
		&model.Server{Common: model.Common{ID: 1, UserID: 1}, Name: "reporter"},
	)
	service := &model.Service{
		Common:      model.Common{ID: 10, UserID: 1},
		Name:        "updatable-service",
		Type:        model.TaskTypeTCPPing,
		Target:      "updatable.example.invalid:443",
		Duration:    3600,
		Cover:       model.ServiceCoverIgnoreAll,
		SkipServers: map[uint64]bool{1: true},
	}
	addServiceMonitorSecurityService(t, ss, service)
	ss.serviceResponseDataStoreLock.Lock()
	ss.serviceStatusToday[service.ID] = &_TodayStatsOfService{Up: 7, Down: 3, Delay: 12.5}
	ss.serviceResponseDataStoreLock.Unlock()
	acceptedReport := make(chan struct{})
	releaseWorker := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorkerHook := func() {
		releaseOnce.Do(func() { close(releaseWorker) })
	}
	ss.serviceReportValidatedHook = func(serviceID uint64) {
		if serviceID == service.ID {
			close(acceptedReport)
			<-releaseWorker
		}
	}
	t.Cleanup(func() {
		releaseWorkerHook()
		ss.Close()
	})

	// When
	ss.Dispatch(serviceMonitorResult(1, service.ID, model.TaskTypeTCPPing, true))
	select {
	case <-acceptedReport:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	updatedService := *service
	updatedService.Name = "updated-service"
	updatedService.SkipServers = map[uint64]bool{}
	if err := ss.Update(&updatedService); err != nil {
		t.Fatal(err)
	}
	releaseWorkerHook()
	ss.Close()

	// Then
	var historyCount int64
	if err := DB.Model(&model.ServiceHistory{}).
		Where("service_id = ? AND server_id = ?", service.ID, 1).
		Count(&historyCount).Error; err != nil {
		t.Fatal(err)
	}
	if historyCount != 0 {
		t.Fatalf("expected updated service to write zero per-reporter history rows, got %d", historyCount)
	}
	ss.serviceResponseDataStoreLock.RLock()
	_, pingCached := ss.serviceResponsePing[service.ID]
	stats := ss.serviceStatusToday[service.ID]
	ss.serviceResponseDataStoreLock.RUnlock()
	if pingCached {
		t.Fatal("expected updated service report to leave no ping cache entry")
	}
	if stats == nil || stats.Up != 7 || stats.Down != 3 || stats.Delay != 12.5 {
		t.Fatalf("expected existing service stats to remain unchanged, got %+v", stats)
	}
	currentService, ok := ss.Get(service.ID)
	if !ok || currentService.Name != updatedService.Name || currentService.SkipServers[1] {
		t.Fatalf("expected updated service configuration, got %+v", currentService)
	}
}

func TestServiceSentinelLoadStatsFollowsLifecycleLockOrder(t *testing.T) {
	// Given
	ss := &ServiceSentinel{
		serviceStatusToday:       make(map[uint64]*_TodayStatsOfService),
		serviceResponseDataStore: make(map[uint64]serviceResponseData),
		services:                 make(map[uint64]*model.Service),
		monthlyStatus:            make(map[uint64]*serviceResponseItem),
	}
	ss.loadStatsResponseLockedHook = func() {
		if ss.serviceResponseDataStoreLock.TryLock() {
			ss.serviceResponseDataStoreLock.Unlock()
			t.Fatal("LoadStats invoked the hook before acquiring the response read lock")
		}
		if !ss.monthlyStatusLock.TryLock() {
			t.Fatal("LoadStats acquired monthlyStatusLock before the response lock hook")
		}
		ss.monthlyStatusLock.Unlock()
		if !ss.servicesLock.TryLock() {
			t.Fatal("LoadStats acquired servicesLock before the response lock hook")
		}
		ss.servicesLock.Unlock()
	}

	// When / Then
	ss.LoadStats()
}

func TestServiceSentinelWorkerHoldsResponseLockDuringTLSSideEffects(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ss := newServiceMonitorSecurityHarness(t,
		&model.Server{Common: model.Common{ID: 1, UserID: 1}, Name: "reporter"},
	)
	service := &model.Service{
		Common:      model.Common{ID: 10, UserID: 1},
		Name:        "tls-service",
		Type:        model.TaskTypeHTTPGet,
		Target:      "https://tls.example.invalid",
		Duration:    3600,
		Cover:       model.ServiceCoverIgnoreAll,
		SkipServers: map[uint64]bool{1: true},
	}
	addServiceMonitorSecurityService(t, ss, service)
	tlsSideEffectsReady := make(chan struct{})
	releaseTLSSideEffects := make(chan struct{})
	var releaseOnce sync.Once
	releaseTLSSideEffectsHook := func() {
		releaseOnce.Do(func() { close(releaseTLSSideEffects) })
	}
	ss.serviceReportBeforeTLSSideEffectsHook = func(serviceID uint64) {
		if serviceID == service.ID {
			close(tlsSideEffectsReady)
			<-releaseTLSSideEffects
		}
	}
	t.Cleanup(func() {
		releaseTLSSideEffectsHook()
		ss.Close()
	})
	report := serviceMonitorResult(1, service.ID, model.TaskTypeHTTPGet, true)
	report.Data.Data = "issuer|2030-01-02 15:04:05 +0000 UTC"

	// When
	ss.Dispatch(report)
	select {
	case <-tlsSideEffectsReady:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	responseLockAcquired := ss.serviceResponseDataStoreLock.TryLock()
	if responseLockAcquired {
		ss.serviceResponseDataStoreLock.Unlock()
		t.Fatal("worker released the response lock before TLS side effects")
	}
	releaseTLSSideEffectsHook()
	ss.Close()

	// Then
	ss.serviceResponseDataStoreLock.RLock()
	cachedCertificate := ss.tlsCertCache[service.ID]
	ss.serviceResponseDataStoreLock.RUnlock()
	if cachedCertificate != report.Data.Data {
		t.Fatalf("expected TLS cache %q, got %q", report.Data.Data, cachedCertificate)
	}
}
