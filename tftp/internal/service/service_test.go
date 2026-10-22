package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/zerotrustgarage/tools/tftp/internal/client"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReloadRejectsInvalidAndRollsBack(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "images")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "boot"), []byte("abc"), 0600)
	sock, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := sock.LocalAddr().(*net.UDPAddr).Port
	sock.Close()
	c := Config{Root: root, Bind: "127.0.0.1", Port: port, Duration: "1m", Indefinite: true}
	path := filepath.Join(base, "config.json")
	write := func(c Config) {
		b, _ := json.Marshal(c)
		tmp := path + ".tmp"
		os.WriteFile(tmp, b, 0600)
		if e := os.Rename(tmp, path); e != nil {
			t.Fatal(e)
		}
	}
	write(c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reload := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, path, reload, io.Discard) }()
	get := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		var b bytes.Buffer
		_, e := client.Get(ctx, client.Config{Address: fmt.Sprintf("127.0.0.1:%d", port), Name: "boot", Timeout: 1}, &b)
		return e
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if get() == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not start")
		}
	}
	bad := c
	bad.Duration = "invalid"
	write(bad)
	reload <- struct{}{}
	time.Sleep(20 * time.Millisecond)
	if e = get(); e != nil {
		t.Fatal("invalid reload stopped service", e)
	}
	busy, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer busy.Close()
	bad = c
	bad.Port = busy.LocalAddr().(*net.UDPAddr).Port
	write(bad)
	reload <- struct{}{}
	time.Sleep(50 * time.Millisecond)
	if e = get(); e != nil {
		t.Fatal("failed binding did not roll back", e)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service did not stop")
	}
}
func TestTLSForwardFramingAndTrust(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	pair := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	l, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	frames := make(chan string, 1)
	go func() {
		conn, e := l.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(conn)
		var size int
		fmt.Fscanf(r, "%d ", &size)
		if size < 1 || size > 65536 {
			return
		}
		b := make([]byte, size)
		if _, e = io.ReadFull(r, b); e == nil {
			frames <- string(b)
		}
	}()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	addr := fmt.Sprintf("localhost:%d", l.Addr().(*net.TCPAddr).Port)
	f, e := NewForward(Config{SyslogTLS: addr, SyslogCA: ca})
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if _, e = f.Write([]byte("{\"result\":\"complete\"}\n")); e != nil {
		t.Fatal(e)
	}
	select {
	case frame := <-frames:
		if !strings.HasPrefix(frame, "<134>1 ") || !strings.HasSuffix(frame, `{"result":"complete"}`) {
			t.Fatal(frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no frame")
	}
}
