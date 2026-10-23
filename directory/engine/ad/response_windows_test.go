//go:build windows

package ad

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// These tests exercise WinLDAP's real LDAP message parser against a bounded
// loopback RFC 4511 fixture. They do not use Open, Negotiate, or credentials.
func TestWinLDAPSearchAndGetReadUnicodeAndMultivalueAttributes(t *testing.T) {
	fixture := startLDAPFixture(t, func() ([][]byte, int) {
		return [][]byte{ldapEntry("CN=Zoë,OU=People,DC=example,DC=test",
			ldapAttribute("cn", "Zoë"),
			ldapAttribute("displayName", "Zoë 栗山"),
			ldapAttribute("mail", "zoe@example.test", "z.kuriyama@example.test"),
			ldapAttribute("memberOf", "CN=Operators,DC=example,DC=test", "CN=Auditors,DC=example,DC=test"),
		)}, 0
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := fixture.client.Search(ctx, SearchRequest{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Objects) != 1 {
		t.Fatalf("Search returned %d objects, want 1", len(got.Objects))
	}
	assertUnicodeEntry(t, got.Objects[0])

	entry, err := fixture.client.Get(ctx, "CN=Zoë,OU=People,DC=example,DC=test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertUnicodeEntry(t, entry)
}

func TestWinLDAPGetMapsNoSuchObjectToNotFound(t *testing.T) {
	fixture := startLDAPFixture(t, func() ([][]byte, int) {
		return nil, 32 // LDAP_NO_SUCH_OBJECT
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fixture.client.Get(ctx, "CN=Missing,DC=example,DC=test")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get error = %v, want errors.Is(ErrNotFound)", err)
	}
}

func TestWinLDAPGetRejectsPartialRangedMemberOf(t *testing.T) {
	fixture := startLDAPFixture(t, func() ([][]byte, int) {
		return [][]byte{ldapEntry("CN=User,DC=example,DC=test",
			ldapAttribute("memberOf;range=0-1499", "CN=First,DC=example,DC=test"),
		)}, 0
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fixture.client.Get(ctx, "CN=User,DC=example,DC=test")
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Get error = %v, want errors.Is(ErrIncomplete)", err)
	}
}

func TestWinLDAPGetAcceptsTerminalRangedMemberOf(t *testing.T) {
	fixture := startLDAPFixture(t, func() ([][]byte, int) {
		return [][]byte{ldapEntry("CN=User,DC=example,DC=test",
			ldapAttribute("memberOf;range=0-*", "CN=First,DC=example,DC=test", "CN=Last,DC=example,DC=test"),
		)}, 0
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entry, err := fixture.client.Get(ctx, "CN=User,DC=example,DC=test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := []string{"CN=First,DC=example,DC=test", "CN=Last,DC=example,DC=test"}
	if values := entry.Values("memberOf"); !equalStrings(values, want) {
		t.Fatalf("memberOf values = %#v, want %#v", values, want)
	}
}

func TestWinLDAPBinaryAttributeRejectsMalformedSID(t *testing.T) {
	malformedSID := make([]byte, 12)
	malformedSID[0] = 0 // invalid SID revision
	malformedSID[1] = 1 // one sub-authority
	malformedSID[7] = 5 // identifier authority
	// The length is otherwise plausible, so revision validation rejects it.
	fixture := startLDAPFixture(t, func() ([][]byte, int) {
		return [][]byte{ldapEntry("CN=User,DC=example,DC=test", ldapBinaryAttribute("objectSid", malformedSID))}, 0
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fixture.client.binaryAttribute(ctx, "CN=User,DC=example,DC=test", "objectSid")
	if err == nil || !strings.Contains(err.Error(), "malformed SID") {
		t.Fatalf("binaryAttribute error = %v, want malformed SID rejection", err)
	}
}

func TestWinLDAPBinaryAttributeRejectsPartialRangedTokenGroups(t *testing.T) {
	fixture := startLDAPFixture(t, func() ([][]byte, int) {
		return [][]byte{ldapEntry("CN=User,DC=example,DC=test",
			ldapBinaryAttribute("tokenGroups;range=0-1499", []byte{1}),
		)}, 0
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fixture.client.binaryAttribute(ctx, "CN=User,DC=example,DC=test", "tokenGroups")
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("binaryAttribute error = %v, want errors.Is(ErrIncomplete)", err)
	}
}

func assertUnicodeEntry(t *testing.T, got Object) {
	t.Helper()
	if got.DN != "CN=Zoë,OU=People,DC=example,DC=test" {
		t.Errorf("DN = %q", got.DN)
	}
	if got.First("displayName") != "Zoë 栗山" {
		t.Errorf("displayName = %q", got.First("displayName"))
	}
	wantMail := []string{"zoe@example.test", "z.kuriyama@example.test"}
	if values := got.Values("mail"); !equalStrings(values, wantMail) {
		t.Errorf("mail values = %#v, want %#v", values, wantMail)
	}
	wantGroups := []string{"CN=Operators,DC=example,DC=test", "CN=Auditors,DC=example,DC=test"}
	if values := got.Values("memberOf"); !equalStrings(values, wantGroups) {
		t.Errorf("memberOf values = %#v, want %#v", values, wantGroups)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type ldapFixture struct {
	client *windowsClient
}

func startLDAPFixture(t *testing.T, respond func() (entries [][]byte, resultCode int)) *ldapFixture {
	return startLDAPFixtureWithRequest(t, func(_ ldapSearchRequest) (entries [][]byte, resultCode int) {
		return respond()
	})
}

// ldapSearchRequest contains the fields range tests need to verify in requests
// emitted by WinLDAP. Attribute names are kept as sent by the client.
type ldapSearchRequest struct {
	base       string
	scope      int64
	attributes []string
}

func startLDAPRangeFixture(t *testing.T, respond func(ldapSearchRequest) (entries [][]byte, resultCode int)) *ldapFixture {
	return startLDAPFixtureWithRequest(t, respond)
}

func startLDAPFixtureWithRequest(t *testing.T, respond func(ldapSearchRequest) (entries [][]byte, resultCode int)) *ldapFixture {
	t.Helper()
	if err := loadSystemLDAP(); err != nil {
		t.Fatalf("loadSystemLDAP: %v", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen loopback: %v", err)
	}
	serverDone := make(chan error, 1)
	accepted := make(chan net.Conn, 1)
	go func() { serverDone <- serveLDAPFixture(listener, accepted, respond) }()

	var client *windowsClient
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if client != nil {
			_ = client.Close()
		}
		_ = listener.Close()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Errorf("loopback LDAP fixture: %v", err)
			}
			return
		case <-time.After(5 * time.Second):
			select {
			case conn := <-accepted:
				_ = conn.Close()
			default:
			}
		}
		select {
		case err := <-serverDone:
			if err != nil {
				t.Errorf("loopback LDAP fixture: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("timed out stopping loopback LDAP fixture")
		}
	}
	t.Cleanup(stop)

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	port, _ := strconv.Atoi(portText)
	hostp, err := syscall.UTF16PtrFromString(host)
	if err != nil {
		t.Fatalf("UTF-16 loopback host: %v", err)
	}
	r, _, callErr := ldapInit.Call(uintptr(unsafe.Pointer(hostp)), uintptr(port))
	if r == 0 {
		t.Fatalf("ldap_initW: %v", callErr)
	}
	client = &windowsClient{conn: r, base: "DC=example,DC=test", timeout: 4 * time.Second}
	cleanupHandle := func(err error) {
		stop()
		t.Fatalf("initialize loopback LDAP handle: %v", err)
	}
	if err := client.setInt(ldapOptProtocolVersion, 3); err != nil {
		cleanupHandle(err)
	}
	if err := client.setFlag(ldapOptReferrals, false); err != nil {
		cleanupHandle(err)
	}
	if err := client.setInt(ldapOptTimeLimit, 4); err != nil {
		cleanupHandle(err)
	}
	if err := client.setInt(ldapOptSendTimeout, 4); err != nil {
		cleanupHandle(err)
	}
	connectTimeout := ldapTimeval{Seconds: 3}
	if code, _, e := ldapConnect.Call(client.conn, uintptr(unsafe.Pointer(&connectTimeout))); code != ldapSuccess {
		cleanupHandle(fmt.Errorf("ldap_connect: %d (%v)", code, e))
	}
	// This direct client setup uses only an anonymous bind against loopback.
	// Public Open and Windows Negotiate are not involved.
	if code, _, e := ldapBind.Call(client.conn, 0, 0, 0x80); code != ldapSuccess {
		cleanupHandle(fmt.Errorf("anonymous ldap_bind_sW: %d (%v)", code, e))
	}
	return &ldapFixture{client: client}
}

func serveLDAPFixture(listener net.Listener, accepted chan<- net.Conn, respond func(ldapSearchRequest) (entries [][]byte, resultCode int)) error {
	conn, err := listener.Accept()
	if err != nil {
		return nil // cleanup closes the listener if client setup fails
	}
	accepted <- conn
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	for {
		message, err := readLDAPMessage(conn)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		id, operationTag, operation, err := ldapMessageOperation(message)
		if err != nil {
			return err
		}
		switch operationTag {
		case 0x42: // UnbindRequest has no response.
			return nil
		case 0x60: // BindRequest.
			if err := validateAnonymousBind(operation); err != nil {
				return err
			}
			if err := writeLDAPMessage(conn, ldapMessage(id, ldapResult(0x61, 0))); err != nil {
				return err
			}
		case 0x63: // SearchRequest.
			request, err := parseLDAPSearchRequest(operation)
			if err != nil {
				return err
			}
			entries, result := respond(request)
			for _, entry := range entries {
				if err := writeLDAPMessage(conn, ldapMessage(id, entry)); err != nil {
					return err
				}
			}
			if err := writeLDAPMessage(conn, ldapMessage(id, ldapResult(0x65, result))); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected LDAP operation tag %#x", operationTag)
		}
	}
}

func parseLDAPSearchRequest(operation []byte) (ldapSearchRequest, error) {
	outer, err := berChildren(operation)
	if err != nil || len(outer) != 1 || outer[0].tag != 0x63 {
		return ldapSearchRequest{}, errors.New("fixture received invalid SearchRequest")
	}
	fields, err := berChildren(outer[0].value)
	if err != nil || len(fields) < 8 || fields[0].tag != 0x04 || fields[1].tag != 0x0a || fields[7].tag != 0x30 {
		return ldapSearchRequest{}, errors.New("fixture received malformed SearchRequest fields")
	}
	attributeElements, err := berChildren(fields[7].value)
	if err != nil {
		return ldapSearchRequest{}, fmt.Errorf("fixture received malformed requested attributes: %w", err)
	}
	request := ldapSearchRequest{base: string(fields[0].value), scope: decodeBERInteger(fields[1].value)}
	for _, attribute := range attributeElements {
		if attribute.tag != 0x04 {
			return ldapSearchRequest{}, errors.New("fixture received non-octet requested attribute")
		}
		request.attributes = append(request.attributes, string(attribute.value))
	}
	return request, nil
}

func validateAnonymousBind(operation []byte) error {
	outer, err := berChildren(operation)
	if err != nil || len(outer) != 1 || outer[0].tag != 0x60 {
		return errors.New("fixture received an invalid BindRequest")
	}
	fields, err := berChildren(outer[0].value)
	if err != nil || len(fields) != 3 || fields[0].tag != 0x02 || fields[1].tag != 0x04 || fields[2].tag != 0x80 {
		return errors.New("fixture accepts only LDAPv3 anonymous simple bind")
	}
	if decodeBERInteger(fields[0].value) != 3 || len(fields[1].value) != 0 || len(fields[2].value) != 0 {
		return errors.New("fixture accepts only LDAPv3 anonymous simple bind")
	}
	return nil
}

func readLDAPMessage(r io.Reader) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	if header[0] != 0x30 {
		return nil, fmt.Errorf("LDAP message tag %#x, want sequence", header[0])
	}
	length := int(header[1])
	encodedLength := []byte{header[1]}
	if header[1]&0x80 != 0 {
		count := int(header[1] & 0x7f)
		if count == 0 || count > 4 {
			return nil, fmt.Errorf("invalid BER length-octet count %d", count)
		}
		longForm := make([]byte, count)
		if _, err := io.ReadFull(r, longForm); err != nil {
			return nil, err
		}
		encodedLength = append(encodedLength, longForm...)
		length = 0
		for _, b := range longForm {
			length = length<<8 | int(b)
		}
	}
	if length > 1<<20 {
		return nil, fmt.Errorf("LDAP message exceeds fixture limit: %d bytes", length)
	}
	message := append([]byte{header[0]}, encodedLength...)
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return append(message, body...), nil
}

func ldapMessageOperation(message []byte) (int, byte, []byte, error) {
	outer, err := berChildren(message)
	if err != nil || len(outer) != 1 || outer[0].tag != 0x30 {
		return 0, 0, nil, fmt.Errorf("invalid LDAPMessage envelope")
	}
	fields, err := berChildren(outer[0].value)
	if err != nil || len(fields) < 2 || fields[0].tag != 0x02 {
		return 0, 0, nil, fmt.Errorf("invalid LDAPMessage fields")
	}
	return int(decodeBERInteger(fields[0].value)), fields[1].tag, fields[1].full, nil
}

type berElement struct {
	tag   byte
	value []byte
	full  []byte
}

func berChildren(data []byte) ([]berElement, error) {
	var out []berElement
	for len(data) > 0 {
		if len(data) < 2 {
			return nil, errors.New("truncated BER element")
		}
		tag := data[0]
		length, header, err := berLength(data[1:])
		if err != nil || length < 0 || 1+header+length > len(data) {
			return nil, errors.New("invalid BER element length")
		}
		full := data[:1+header+length]
		out = append(out, berElement{tag: tag, value: full[1+header:], full: full})
		data = data[1+header+length:]
	}
	return out, nil
}

func berLength(encoded []byte) (int, int, error) {
	first := encoded[0]
	if first&0x80 == 0 {
		return int(first), 1, nil
	}
	count := int(first & 0x7f)
	if count == 0 || count > 4 || len(encoded) < 1+count {
		return 0, 0, errors.New("invalid BER length")
	}
	length := 0
	for _, b := range encoded[1 : 1+count] {
		length = length<<8 | int(b)
	}
	return length, 1 + count, nil
}

func decodeBERInteger(data []byte) int64 {
	var value int64
	for _, b := range data {
		value = value<<8 | int64(b)
	}
	if len(data) > 0 && data[0]&0x80 != 0 {
		value -= 1 << (uint(len(data)) * 8)
	}
	return value
}

func ldapEntry(dn string, attributes ...[]byte) []byte {
	return berTLV(0x64, berJoin(berOctet(dn), berTLV(0x30, berJoin(attributes...))))
}

func ldapAttribute(name string, values ...string) []byte {
	encoded := make([][]byte, 0, len(values))
	for _, value := range values {
		encoded = append(encoded, berOctet(value))
	}
	return berTLV(0x30, berJoin(berOctet(name), berTLV(0x31, berJoin(encoded...))))
}

func ldapBinaryAttribute(name string, values ...[]byte) []byte {
	encoded := make([][]byte, 0, len(values))
	for _, value := range values {
		encoded = append(encoded, berOctetBytes(value))
	}
	return berTLV(0x30, berJoin(berOctet(name), berTLV(0x31, berJoin(encoded...))))
}

func ldapResult(tag byte, resultCode int) []byte {
	return berTLV(tag, berJoin(berInteger(0x0a, int64(resultCode)), berOctet(""), berOctet("")))
}

func ldapMessage(id int, operation []byte) []byte {
	return berTLV(0x30, berJoin(berInteger(0x02, int64(id)), operation))
}

func writeLDAPMessage(w io.Writer, message []byte) error {
	if len(message) > 1<<20 {
		return fmt.Errorf("LDAP fixture response exceeds 1 MiB")
	}
	for len(message) > 0 {
		n, err := w.Write(message)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		message = message[n:]
	}
	return nil
}

func berOctet(value string) []byte { return berOctetBytes([]byte(value)) }

func berOctetBytes(value []byte) []byte { return berTLV(0x04, value) }

func berInteger(tag byte, value int64) []byte {
	var bytes [8]byte
	binary.BigEndian.PutUint64(bytes[:], uint64(value))
	start := 0
	for start < len(bytes)-1 {
		if bytes[start] == 0 && bytes[start+1]&0x80 == 0 || bytes[start] == 0xff && bytes[start+1]&0x80 != 0 {
			start++
			continue
		}
		break
	}
	return berTLV(tag, bytes[start:])
}

func berJoin(parts ...[]byte) []byte {
	var out []byte
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func berTLV(tag byte, value []byte) []byte {
	out := []byte{tag}
	length := len(value)
	if length < 128 {
		out = append(out, byte(length))
	} else {
		var encoded [4]byte
		i := len(encoded)
		for n := length; n > 0; n >>= 8 {
			i--
			encoded[i] = byte(n)
		}
		count := len(encoded) - i
		out = append(out, 0x80|byte(count))
		out = append(out, encoded[i:]...)
	}
	return append(out, value...)
}
