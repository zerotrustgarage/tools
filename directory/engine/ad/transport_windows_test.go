//go:build windows

package ad

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// This exercises WinLDAP's real LDAPS certificate validation without a domain
// controller, trust-store changes, or credentials. The generated certificate
// is valid for the loopback IP but is self-signed and untrusted. The server
// never speaks LDAP or accepts a bind.
func TestOpenRejectsUntrustedLoopbackLDAPSCertificate(t *testing.T) {
	certificate, err := untrustedLoopbackCertificate()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	type serverObservation struct {
		clientHelloSeen bool
		handshakeErr    error
		appDataRead     bool
		acceptErr       error
	}
	serverDone := make(chan serverObservation, 1)
	go func() {
		var observed serverObservation
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			observed.acceptErr = acceptErr
			serverDone <- observed
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		secure := tls.Server(conn, &tls.Config{
			Certificates: []tls.Certificate{certificate},
			// Unlike GetCertificate, this callback also runs for an IP
			// address, for which a client correctly sends no SNI name.
			GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
				observed.clientHelloSeen = true
				return nil, nil
			},
		})
		observed.handshakeErr = secure.Handshake()
		if observed.handshakeErr == nil {
			// Do not parse or answer an LDAP bind. One byte is enough to
			// establish whether the client sent application data at all.
			_ = secure.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, _ := secure.Read([]byte{0})
			observed.appDataRead = n != 0
		}
		serverDone <- observed
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	client, openErr := Open(ctx, Config{
		Server:  listener.Addr().String(),
		BaseDN:  "DC=invalid,DC=example",
		LDAPS:   true,
		Timeout: 5 * time.Second,
	})
	if client != nil {
		_ = client.Close()
	}
	if openErr == nil {
		t.Fatal("Open accepted an untrusted LDAPS certificate")
	}

	// Closing the listener also releases the server goroutine if Open failed
	// before it attempted a network connection.
	_ = listener.Close()
	var observed serverObservation
	select {
	case observed = <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for loopback TLS server")
	}
	if observed.acceptErr != nil {
		t.Fatalf("loopback server did not accept the client: %v", observed.acceptErr)
	}
	if !observed.clientHelloSeen {
		t.Fatal("WinLDAP did not reach the TLS handshake")
	}
	if observed.handshakeErr == nil {
		t.Fatal("WinLDAP completed TLS with the untrusted certificate")
	}
	if observed.appDataRead {
		t.Fatal("WinLDAP sent LDAP application data before rejecting the certificate")
	}
}

func untrustedLoopbackCertificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	privateKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}),
	)
}
