package utils

import (
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestBuildRestrictedHTTPClientPreservesHostnameAsTLSServerName(t *testing.T) {
	// Construct a hostname URL paired with an arbitrary public IP so we exercise
	// the SNI preservation path without depending on live DNS in unit tests.
	parsed, err := url.Parse("https://example.com/webhook")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	pinnedIP := net.ParseIP("1.1.1.1")
	if pinnedIP == nil {
		t.Fatalf("expected valid pinned IP")
	}

	client := buildRestrictedHTTPClient(parsed, pinnedIP, false)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if transport.TLSClientConfig == nil {
		t.Fatalf("expected TLSClientConfig to be set")
	}
	// SNI must come from the original URL hostname so the certificate validates
	// the intended hostname, not the pinned dial IP.
	if got := transport.TLSClientConfig.ServerName; got != "example.com" {
		t.Fatalf("expected ServerName example.com, got %q", got)
	}
	if transport.TLSClientConfig.ServerName == pinnedIP.String() {
		t.Fatalf("ServerName must not be the pinned IP, got %q", transport.TLSClientConfig.ServerName)
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected verifyTLS path (InsecureSkipVerify=false)")
	}
}

func TestBuildRestrictedHTTPClientHonorsSkipVerifyTLS(t *testing.T) {
	parsed, _ := url.Parse("https://example.com/webhook")
	client := buildRestrictedHTTPClient(parsed, net.ParseIP("1.1.1.1"), true)
	transport := client.Transport.(*http.Transport)
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify=true when skipVerifyTLS=true")
	}
}

func TestBuildRestrictedHTTPClientRejectsRedirects(t *testing.T) {
	parsed, _ := url.Parse("https://example.com/start")
	client := buildRestrictedHTTPClient(parsed, net.ParseIP("1.1.1.1"), false)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/start", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if err := client.CheckRedirect(req, []*http.Request{req}); err != http.ErrUseLastResponse {
		t.Fatalf("expected ErrUseLastResponse, got %v", err)
	}
}

// TestBuildRestrictedHTTPClientPinsDialToVettedIP confirms DialContext routes
// to the pinned IP even when the request URL uses a different hostname,
// preventing DNS rebinding from retargeting traffic.
func TestBuildRestrictedHTTPClientPinsDialToVettedIP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	accepted := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			accepted <- ""
			return
		}
		accepted <- conn.LocalAddr().String()
		conn.Close()
	}()

	requestURL := "http://example.com:" + port + "/"
	parsed, _ := url.Parse(requestURL)
	pinned := net.ParseIP("127.0.0.1")
	client := buildRestrictedHTTPClient(parsed, pinned, false)
	client.Timeout = 2 * time.Second

	req, _ := http.NewRequest(http.MethodGet, requestURL, nil)
	resp, _ := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}

	select {
	case addr := <-accepted:
		if addr == "" {
			t.Fatalf("listener accept failed")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected dial to reach pinned IP 127.0.0.1:%s, listener did not accept", port)
	}
}
