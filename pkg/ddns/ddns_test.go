package ddns

import (
	"context"
	"net"
	"testing"

	"github.com/libdns/libdns"
	"github.com/miekg/dns"

	"github.com/nezhahq/nezha/model"
)

type testSt struct {
	domain string
	zone   string
	prefix string
}

func TestSplitDomainSOA(t *testing.T) {
	handler := dns.NewServeMux()
	handler.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) > 0 {
			qname := r.Question[0].Name
			soa := &dns.SOA{
				Hdr:     dns.RR_Header{Name: qname, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 300},
				Ns:      "ns.example.com.",
				Mbox:    "admin.example.com.",
				Serial:  1,
				Refresh: 3600,
				Retry:   600,
				Expire:  86400,
				Minttl:  60,
			}
			m.Answer = append(m.Answer, soa)
		}
		_ = w.WriteMsg(m)
	})

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer pc.Close()

	server := &dns.Server{PacketConn: pc, Net: "udp", Handler: handler}
	go func() {
		_ = server.ActivateAndServe()
	}()
	defer server.Shutdown()

	addr := pc.LocalAddr().String()

	cases := []testSt{
		{
			domain: "www.example.co.uk",
			zone:   "example.co.uk.",
			prefix: "www",
		},
		{
			domain: "abc.example.com",
			zone:   "example.com.",
			prefix: "abc",
		},
		{
			domain: "example.com",
			zone:   "example.com.",
			prefix: "",
		},
	}

	ctx := context.WithValue(context.Background(), DNSServerKey{}, []string{addr})
	provider := &Provider{}
	for _, c := range cases {
		prefix, zone, err := provider.splitDomainSOA(ctx, c.domain)
		if err != nil {
			t.Fatalf("Error for domain %s: %s", c.domain, err)
		}
		if prefix != c.prefix {
			t.Fatalf("Expected prefix %s, but got %s", c.prefix, prefix)
		}
		if zone != c.zone {
			t.Fatalf("Expected zone %s, but got %s", c.zone, zone)
		}
	}
}

type MockSetter struct {
	Records        []libdns.Record
	SetCalled      bool
	DelCalled      bool
	DeletedRecords []libdns.Record
}

func (m *MockSetter) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	m.SetCalled = true
	m.Records = append(m.Records, records...)
	return records, nil
}

func (m *MockSetter) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	m.DelCalled = true
	m.DeletedRecords = append(m.DeletedRecords, records...)
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
		t.Fatalf("expected no error for unsupported deleter skip, got %v", err)
	}
}

func TestIndependentDualStackProcessing(t *testing.T) {
	enableIPv4 := true
	enableIPv6 := true
	mockSetter := &MockSetter{}

	provider := &Provider{
		DDNSProfile: &model.DDNSProfile{
			MaxRetries: 1,
			Domains:    []string{"example.com"},
			EnableIPv4: &enableIPv4,
			EnableIPv6: &enableIPv6,
		},
		IPAddrs: &model.IP{
			IPv4Addr: "1.1.1.1",
			IPv6Addr: "2001:db8::1",
		},
		Setter: mockSetter,
	}

	handler := dns.NewServeMux()
	handler.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) > 0 {
			qname := r.Question[0].Name
			soa := &dns.SOA{
				Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 300},
			}
			m.Answer = append(m.Answer, soa)
		}
		_ = w.WriteMsg(m)
	})

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer pc.Close()

	server := &dns.Server{PacketConn: pc, Net: "udp", Handler: handler}
	go func() {
		_ = server.ActivateAndServe()
	}()
	defer server.Shutdown()

	ctx := context.WithValue(context.Background(), DNSServerKey{}, []string{pc.LocalAddr().String()})
	provider.UpdateDomain(ctx)

	if !mockSetter.SetCalled {
		t.Fatalf("expected SetRecords to have been called for dual stack updates")
	}

	if len(mockSetter.Records) != 2 {
		t.Fatalf("expected 2 records (IPv4 and IPv6) to be set, got %d", len(mockSetter.Records))
	}
}

func TestTransientSOAFailureAndRetry(t *testing.T) {
	attempts := 0
	handler := dns.NewServeMux()
	handler.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		attempts++
		m := new(dns.Msg)
		m.SetReply(r)
		_ = w.WriteMsg(m)
	})

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer pc.Close()

	server := &dns.Server{PacketConn: pc, Net: "udp", Handler: handler}
	go func() {
		_ = server.ActivateAndServe()
	}()
	defer server.Shutdown()

	maxRetries := 3
	provider := &Provider{
		DDNSProfile: &model.DDNSProfile{
			MaxRetries: uint64(maxRetries),
			Domains:    []string{"fail.invalid"},
		},
		IPAddrs: &model.IP{IPv4Addr: "1.1.1.1"},
		Setter:  &MockSetter{},
	}

	ctx := context.WithValue(context.Background(), DNSServerKey{}, []string{pc.LocalAddr().String()})
	provider.UpdateDomain(ctx)

	if attempts != maxRetries {
		t.Fatalf("expected exact attempt count of %d, got %d", maxRetries, attempts)
	}
}

func TestGenericRecordDeleterContract(t *testing.T) {
	mockSetter := &MockSetter{}
	provider := &Provider{
		DDNSProfile: &model.DDNSProfile{MaxRetries: 1},
		IPAddrs:     &model.IP{IPv4Addr: ""},
		Setter:      mockSetter,
	}

	err := provider.deleteDomainRecord(context.Background(), "sub", "example.com", "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockSetter.DelCalled {
		t.Fatalf("expected DeleteRecords to be called")
	}

	if len(mockSetter.DeletedRecords) != 1 {
		t.Fatalf("expected 1 record sent for deletion, got %d", len(mockSetter.DeletedRecords))
	}

	rr, ok := mockSetter.DeletedRecords[0].(libdns.RR)
	if !ok {
		t.Fatalf("expected deletion payload to be libdns.RR")
	}

	if rr.Name != "sub" || rr.Type != "A" {
		t.Fatalf("unexpected RR fields: Name=%s, Type=%s", rr.Name, rr.Type)
	}
}
