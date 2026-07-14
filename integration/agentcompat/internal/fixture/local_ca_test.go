package fixture

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFixture_VerifiesLocalhostTLS(t *testing.T) {
	// Given
	fixture, err := NewLocalTLSFixture(time.Now())
	requireNoFixtureError(t, err)
	address, closeServer := startLocalTLSServer(t, fixture)
	defer closeServer()
	client := localTLSClient(fixture.ClientConfig("localhost"), address)

	// When
	response, err := client.Get("https://localhost:" + portOf(t, address) + "/ready")
	requireNoFixtureError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	requireNoFixtureError(t, err)

	// Then
	if response.StatusCode != http.StatusOK || string(body) != "tls-ready" {
		t.Fatalf("TLS response = %d %q", response.StatusCode, body)
	}
	if fixture.ClientConfig("localhost").InsecureSkipVerify {
		t.Fatal("TLS fixture disabled certificate verification")
	}
	if len(fixture.CAPEM()) == 0 || len(fixture.CertificatePEM()) == 0 || len(fixture.PrivateKeyPEM()) == 0 {
		t.Fatal("TLS fixture did not expose certificate material")
	}
	assertLocalCertificateProperties(t, fixture)
}

func assertLocalCertificateProperties(t *testing.T, fixture LocalTLSFixture) {
	t.Helper()
	caBlock, _ := pem.Decode(fixture.CAPEM())
	if caBlock == nil {
		t.Fatal("fixture CA PEM is invalid")
	}
	caCertificate, err := x509.ParseCertificate(caBlock.Bytes)
	requireNoFixtureError(t, err)
	if !caCertificate.IsCA || !caCertificate.BasicConstraintsValid || caCertificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatalf("fixture CA constraints are invalid: %+v", caCertificate)
	}
	leafBlock, _ := pem.Decode(fixture.CertificatePEM())
	if leafBlock == nil {
		t.Fatal("fixture leaf PEM is invalid")
	}
	leafCertificate, err := x509.ParseCertificate(leafBlock.Bytes)
	requireNoFixtureError(t, err)
	if err := leafCertificate.VerifyHostname("localhost"); err != nil {
		t.Fatalf("verify localhost SAN: %v", err)
	}
	if err := leafCertificate.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatalf("verify loopback SAN: %v", err)
	}
}

func TestFixture_RejectsTLSNameMismatch(t *testing.T) {
	// Given
	fixture, err := NewLocalTLSFixture(time.Now())
	requireNoFixtureError(t, err)
	address, closeServer := startLocalTLSServer(t, fixture)
	defer closeServer()
	client := localTLSClient(fixture.ClientConfig("wronghost.invalid"), address)

	// When
	_, err = client.Get("https://wronghost.invalid:" + portOf(t, address) + "/ready")

	// Then
	var hostnameError x509.HostnameError
	if !errors.As(err, &hostnameError) {
		t.Fatalf("TLS mismatch error = %v", err)
	}
}

func startLocalTLSServer(t *testing.T, fixture LocalTLSFixture) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	requireNoFixtureError(t, err)
	tlsListener := fixture.Listener(listener)
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ready" {
			http.NotFound(writer, request)
			return
		}
		_, _ = io.WriteString(writer, "tls-ready")
	})}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(tlsListener) }()
	closeServer := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		requireNoFixtureError(t, server.Shutdown(ctx))
		serveErr := <-serveDone
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			t.Fatalf("serve TLS: %v", serveErr)
		}
	}
	return listener.Addr().String(), closeServer
}

func localTLSClient(config *tls.Config, address string) *http.Client {
	transport := &http.Transport{
		TLSClientConfig: config,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
	}
	return &http.Client{Transport: transport, Timeout: 2 * time.Second}
}

func portOf(t *testing.T, address string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	requireNoFixtureError(t, err)
	return strings.TrimSpace(port)
}
