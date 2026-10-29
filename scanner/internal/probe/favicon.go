package probe

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"mime"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// FaviconInfo fingerprints the icon a web interface serves at /favicon.ico.
// Device families (a printer line, a camera line, a NAS) usually share one icon
// across models and firmware, so the hash identifies the interface without
// reading any page text. Scanner records the hash only. It ships no table that
// maps hashes to products, because no table with a source it may redistribute
// exists to copy from (design.md, SC11); a technician can look a hash up in
// their own tools.
type FaviconInfo struct {
	// SHA256 is the SHA-256 of the icon bytes, lower-case hex. This is the
	// identifier Scanner itself uses (comparisons, reports).
	SHA256 string `json:"sha256"`
	// MMH3 is the 32-bit MurmurHash3 (x86, seed 0) of the icon's MIME base64
	// text (76-character lines, each ending in a newline), as a signed integer.
	// It is the value the common internet-scan search engines index as
	// http.favicon.hash, so a technician can paste it into those tools.
	MMH3        int32  `json:"mmh3"`
	Size        int    `json:"size"`
	ContentType string `json:"content_type,omitempty"`
	// Format is the image type recognized from the first bytes, or "".
	Format string `json:"format,omitempty"`
}

const (
	faviconMaxBody   = 64 * 1024 // bytes of icon that will be hashed; larger icons are not fingerprinted
	faviconMaxHeader = 32 * 1024
)

// FetchFavicon makes one GET of /favicon.ico over an already known web port, on
// plain HTTP or, when useTLS, over TLS. It sends no cookies, credentials or
// referrer, follows no redirect, accepts only a 200 answer whose body is an
// image of at most 64 KiB, and hashes the bytes. Certificate verification is
// off here exactly as for the TLS identification probe: LAN devices use
// self-signed certificates, nothing about the reply is trusted, and no secret
// is ever sent on this connection.
func FetchFavicon(ctx context.Context, addr netip.Addr, port int, useTLS bool, timeout time.Duration) (*FaviconInfo, error) {
	raw, err := DialTCP(ctx, addr, port, timeout)
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = raw.SetDeadline(deadline)
	var conn io.ReadWriter = raw
	if useTLS {
		tc := tls.Client(raw, &tls.Config{
			InsecureSkipVerify: true, // why: inventory fingerprint only; the reply is hashed, never trusted, and no credentials or cookies are sent
			MinVersion:         tls.VersionTLS10,
		})
		if err := tc.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = tc
	}
	req := fmt.Sprintf("GET /favicon.ico HTTP/1.1\r\nHost: %s\r\nUser-Agent: %s\r\nAccept: image/*,*/*;q=0.5\r\nConnection: close\r\n\r\n", hostHeader(addr), UserAgent)
	if _, err := io.WriteString(conn, req); err != nil {
		return nil, err
	}
	// Headers and body share one limit, so a hostile server cannot make the
	// read unbounded whatever it claims in Content-Length or chunk sizes.
	br := bufio.NewReader(io.LimitReader(conn, faviconMaxHeader+faviconMaxBody+1024))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("favicon status %d", resp.StatusCode)
	}
	if resp.ContentLength > faviconMaxBody {
		return nil, errors.New("favicon is larger than the limit")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, faviconMaxBody+1))
	if err != nil {
		return nil, err // a partial body would give a hash that matches nothing
	}
	return fingerprintFavicon(body, resp.Header.Get("Content-Type"))
}

// fingerprintFavicon hashes body if it is an acceptable icon. Many web
// interfaces answer every path with their HTML page; that is not an icon and is
// refused.
func fingerprintFavicon(body []byte, contentType string) (*FaviconInfo, error) {
	if len(body) == 0 {
		return nil, errors.New("empty favicon")
	}
	if len(body) > faviconMaxBody {
		return nil, errors.New("favicon is larger than the limit")
	}
	ctype := ""
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		ctype = CleanText(strings.ToLower(mt), 100)
	}
	format := imageFormat(body)
	if format == "" && !strings.HasPrefix(ctype, "image/") {
		return nil, errors.New("favicon response is not an image")
	}
	sum := sha256.Sum256(body)
	return &FaviconInfo{
		SHA256:      hex.EncodeToString(sum[:]),
		MMH3:        int32(murmur3x86(mimeBase64(body), 0)),
		Size:        len(body),
		ContentType: ctype,
		Format:      format,
	}, nil
}

// imageFormat recognizes the formats favicons come in by their first bytes.
func imageFormat(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0, 0, 1, 0}):
		return "ico"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "gif"
	case bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}):
		return "jpeg"
	case bytes.HasPrefix(b, []byte("BM")) && len(b) > 14:
		return "bmp"
	case len(b) > 12 && bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "webp"
	}
	return ""
}

// mimeBase64 is the text the search-engine favicon hash is taken over: standard
// base64 broken into lines of 76 characters, each (the last included) ending in
// a newline, like Python's base64.encodebytes.
func mimeBase64(b []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(b)
	out := make([]byte, 0, len(enc)+len(enc)/76+2)
	for len(enc) > 76 {
		out = append(out, enc[:76]...)
		out = append(out, '\n')
		enc = enc[76:]
	}
	if len(enc) > 0 {
		out = append(out, enc...)
		out = append(out, '\n')
	}
	return out
}

// murmur3x86 is MurmurHash3_x86_32 (Austin Appleby, public domain).
func murmur3x86(data []byte, seed uint32) uint32 {
	const c1, c2 = 0xcc9e2d51, 0x1b873593
	h := seed
	n := len(data) / 4
	for i := 0; i < n; i++ {
		k := binary.LittleEndian.Uint32(data[i*4:])
		k *= c1
		k = bits.RotateLeft32(k, 15)
		k *= c2
		h ^= k
		h = bits.RotateLeft32(h, 13)
		h = h*5 + 0xe6546b64
	}
	tail := data[n*4:]
	var k uint32
	switch len(tail) {
	case 3:
		k ^= uint32(tail[2]) << 16
		fallthrough
	case 2:
		k ^= uint32(tail[1]) << 8
		fallthrough
	case 1:
		k ^= uint32(tail[0])
		k *= c1
		k = bits.RotateLeft32(k, 15)
		k *= c2
		h ^= k
	}
	h ^= uint32(len(data))
	h ^= h >> 16
	h *= 0x85ebca6b
	h ^= h >> 13
	h *= 0xc2b2ae35
	h ^= h >> 16
	return h
}

// Sanitize bounds a value read from a saved file.
func (f *FaviconInfo) Sanitize() {
	if len(f.SHA256) != 64 || strings.Trim(f.SHA256, "0123456789abcdef") != "" {
		f.SHA256 = ""
	}
	f.ContentType = CleanText(f.ContentType, 100)
	f.Format = CleanText(f.Format, 8)
	if f.Size < 0 || f.Size > faviconMaxBody {
		f.Size = 0
	}
}
