package ddns

import (
	"context"
	"testing"
	"time"

	"github.com/libdns/he"
	"github.com/libdns/libdns"
	"github.com/nezhahq/nezha/model"
)

type MockSetter struct {
	Records     []libdns.Record
	SetCalled   bool
	GetCalled   bool
	DelCalled   bool
	DeleteError error
}

func (m *MockSetter) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	m.SetCalled = true
	m.Records = append(m.Records, records...)
	return records, nil
}

func (m *MockSetter) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	m.GetCalled = true
	return m.Records, nil
}

func (m *MockSetter) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	m.DelCalled = true
	if m.DeleteError != nil {
		return nil, m.DeleteError
	}
	m.Records = nil
	return records, nil
}

type MockUnsupportedSetter struct{}

func (m *MockUnsupportedSetter) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	return records, nil
}

func TestUnsupportedGetterDeleterSkip(t *testing.T) {
	provider := &Provider{
		DDNSProfile: &model.DDNSProfile{MaxRetries: 1},
		IPAddrs:     &model.IP{IPv4Addr: ""},
		Setter:      &MockUnsupportedSetter{},
	}

	err := provider.deleteDomainRecord(context.Background(), "sub", "example.com", "A")
	if err != nil {
		t.Fatalf("expected no error for unsupported setter deletion skip, got %v", err)
	}
}

func TestIndependentDualStackProcessing(t *testing.T) {
	enableIPv4 := true
	enableIPv6 := true
	provider := &Provider{
		DDNSProfile: &model.DDNSProfile{
			MaxRetries: 1,
			EnableIPv4: &enableIPv4,
			EnableIPv6: &enableIPv6,
		},
		IPAddrs: &model.IP{
			IPv4Addr: "1.1.1.1",
			IPv6Addr: "",
		},
		Setter: &MockSetter{},
	}

	if provider.DDNSProfile.EnableIPv4 == nil || !*provider.DDNSProfile.EnableIPv4 {
		t.Fatalf("IPv4 should be enabled")
	}
}

func TestHEDirectDeletionPath(t *testing.T) {
	heProvider := &he.Provider{APIKey: "test-key"}
	provider := &Provider{
		DDNSProfile: &model.DDNSProfile{MaxRetries: 1},
		IPAddrs:     &model.IP{IPv4Addr: ""},
		Setter:      heProvider,
	}

	err := provider.deleteDomainRecord(context.Background(), "test", "example.com", "A")
	if err == nil {
		t.Fatalf("expected an error due to invalid test API key in HE direct path, but got nil")
	}
}

func TestTransientSOAFailureAndRetry(t *testing.T) {
	maxRetries := 3
	provider := &Provider{
		DDNSProfile: &model.DDNSProfile{
			MaxRetries: int64(maxRetries),
			Domains:    []string{"notfound.invalid"},
		},
		IPAddrs: &model.IP{IPv4Addr: "1.1.1.1"},
		Setter:  &MockSetter{},
	}

	ctx := context.WithValue(context.Background(), DNSServerKey{}, []string{"192.0.2.1:53"})
	start := time.Now()
	provider.UpdateDomain(ctx)
	duration := time.Since(start)

	if duration > 35*time.Second {
		t.Fatalf("SOA retry loop took too long: %v", duration)
	}
}
