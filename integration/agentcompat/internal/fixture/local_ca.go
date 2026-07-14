package fixture

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"time"
)

type LocalTLSFixture struct {
	certificate    tls.Certificate
	rootCAs        *x509.CertPool
	caPEM          []byte
	certificatePEM []byte
	privateKeyPEM  []byte
}

func NewLocalTLSFixture(now time.Time) (LocalTLSFixture, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return LocalTLSFixture{}, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "agentcompat local CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return LocalTLSFixture{}, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return LocalTLSFixture{}, err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caTemplate, &leafKey.PublicKey, caKey)
	if err != nil {
		return LocalTLSFixture{}, err
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return LocalTLSFixture{}, err
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return LocalTLSFixture{}, err
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caPEM) {
		return LocalTLSFixture{}, errors.New("append local CA certificate")
	}
	return LocalTLSFixture{
		certificate:    certificate,
		rootCAs:        rootCAs,
		caPEM:          caPEM,
		certificatePEM: certificatePEM,
		privateKeyPEM:  privateKeyPEM,
	}, nil
}

func (fixture LocalTLSFixture) ClientConfig(serverName string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    fixture.rootCAs.Clone(),
		ServerName: serverName,
	}
}

func (fixture LocalTLSFixture) Listener(listener net.Listener) net.Listener {
	return tls.NewListener(listener, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{fixture.certificate},
	})
}

func (fixture LocalTLSFixture) CAPEM() []byte {
	return append([]byte(nil), fixture.caPEM...)
}

func (fixture LocalTLSFixture) CertificatePEM() []byte {
	return append([]byte(nil), fixture.certificatePEM...)
}

func (fixture LocalTLSFixture) PrivateKeyPEM() []byte {
	return append([]byte(nil), fixture.privateKeyPEM...)
}
