package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testICO is 200 bytes starting with the ICO signature. The expected values
// below come from an independent implementation (Python's base64.encodebytes
// and a separate MurmurHash3), not from this package.
func testICO() []byte {
	b := []byte{0, 0, 1, 0}
	for i := 0; i < 196; i++ {
		b = append(b, byte((i*7+3)%256))
	}
	return b
}

const (
	testICOBase64 = "AAABAAMKERgfJi00O0JJUFdeZWxzeoGIj5adpKuyucDHztXc4+rx+P8GDRQbIikwNz5FTFNaYWhv\n" +
		"dn2Ei5KZoKeutbzDytHY3+bt9PsCCRAXHiUsMzpBSE9WXWRrcnmAh46VnKOqsbi/xs3U2+Lp8Pf+\n" +
		"BQwTGiEoLzY9REtSWWBnbnV8g4qRmJ+mrbS7wsnQ197l7PP6AQgPFh0kKzI5QEdOVVxjanF4f4aN\n" +
		"lJuiqbC3vsXM09rh6O/2/QQLEhkgJy41PENKUVg=\n"
	testICOSHA256 = "33d499a752b8bfaddefd2358485f5e2014da2570d1e5f8b2e94b5cd87c0b6cb8"
	testICOMMH3   = -1338840234
)

func TestMurmur3Vectors(t *testing.T) {
	// Published MurmurHash3_x86_32 test vectors.
	for _, c := range []struct {
		in   string
		seed uint32
		want uint32
	}{
		{"", 0, 0}, {"", 1, 0x514e28b7},
		{"aaaa", 0x9747b28c, 0x5a97808a},
		{"Hello, world!", 0x9747b28c, 0x24884cba},
		{"The quick brown fox jumps over the lazy dog", 0x9747b28c, 0x2fa826cd},
		{"The quick brown fox jumps over the lazy dog", 0, 0x2e4ff723},
		{"abc", 0, 0xb3dd93fa},
	} {
		if got := murmur3x86([]byte(c.in), c.seed); got != c.want {
			t.Errorf("murmur3(%q, %#x) = %#x, want %#x", c.in, c.seed, got, c.want)
		}
	}
}

func TestFaviconHashes(t *testing.T) {
	if got := string(mimeBase64(testICO())); got != testICOBase64 {
		t.Fatalf("base64 text differs:\n%q\n%q", got, testICOBase64)
	}
	if mimeBase64(nil) != nil && len(mimeBase64(nil)) != 0 {
		t.Fatal("empty input should give empty text")
	}
	// A 57-byte input encodes to exactly one 76-character line.
	if got := string(mimeBase64(make([]byte, 57))); len(got) != 77 || got[76] != '\n' || strings.Count(got, "\n") != 1 {
		t.Fatalf("line break rule: %q", got)
	}
	info, err := fingerprintFavicon(testICO(), "image/x-icon; charset=binary")
	if err != nil {
		t.Fatal(err)
	}
	if info.SHA256 != testICOSHA256 || info.MMH3 != testICOMMH3 || info.Size != 200 || info.ContentType != "image/x-icon" || info.Format != "ico" {
		t.Fatalf("%+v", info)
	}
}

func TestFingerprintFaviconRefusals(t *testing.T) {
	html := []byte("<!doctype html><html><title>Login</title></html>")
	if _, err := fingerprintFavicon(html, "text/html"); err == nil {
		t.Error("HTML catch-all page accepted as an icon")
	}
	if _, err := fingerprintFavicon(html, ""); err == nil {
		t.Error("HTML with no content type accepted")
	}
	if _, err := fingerprintFavicon(nil, "image/x-icon"); err == nil {
		t.Error("empty body accepted")
	}
	if _, err := fingerprintFavicon(make([]byte, faviconMaxBody+1), "image/x-icon"); err == nil {
		t.Error("oversized body accepted")
	}
	// An image served with a wrong content type is still an image by its bytes.
	if info, err := fingerprintFavicon(testICO(), "text/plain"); err != nil || info.Format != "ico" {
		t.Errorf("%+v %v", info, err)
	}
	// An SVG is accepted by its content type.
	if info, err := fingerprintFavicon([]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), "image/svg+xml"); err != nil || info.Format != "" {
		t.Errorf("%+v %v", info, err)
	}
	// Hostile content type text is cleaned.
	if info, err := fingerprintFavicon(testICO(), "image/\x1b[31mx‮"); err == nil && strings.ContainsAny(info.ContentType, "\x1b‮") {
		t.Errorf("content type %q", info.ContentType)
	}
}

func TestImageFormats(t *testing.T) {
	cases := map[string][]byte{
		"ico": {0, 0, 1, 0, 1}, "png": []byte("\x89PNG\r\n\x1a\nxxxx"), "gif": []byte("GIF89a...."),
		"jpeg": {0xff, 0xd8, 0xff, 0xe0}, "bmp": append([]byte("BM"), make([]byte, 20)...),
		"webp": append(append([]byte("RIFF\x00\x00\x00\x00WEBP"), 0), 0),
	}
	for want, b := range cases {
		if got := imageFormat(b); got != want {
			t.Errorf("%s: got %q", want, got)
		}
	}
	for _, b := range [][]byte{nil, []byte("<html>"), []byte("BM"), []byte("RIFF....WAVE....")} {
		if got := imageFormat(b); got != "" {
			t.Errorf("%q recognized as %q", b, got)
		}
	}
}

func hostPort(t *testing.T, s *httptest.Server) (addrPort string) {
	t.Helper()
	return strings.TrimPrefix(strings.TrimPrefix(s.URL, "https://"), "http://")
}

func fetch(t *testing.T, s *httptest.Server, useTLS bool) (*FaviconInfo, error) {
	t.Helper()
	ap := hostPort(t, s)
	return FetchFavicon(context.Background(), loopback, portOf(t, ap), useTLS, time.Second)
}

func TestFetchFaviconPlainHTTP(t *testing.T) {
	var requests atomic.Int32
	var bad atomic.Value
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case r.Method != http.MethodGet || r.URL.Path != "/favicon.ico":
			bad.Store("request " + r.Method + " " + r.URL.String())
		case r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "":
			bad.Store("credentials or referrer sent")
		case !strings.HasPrefix(r.Header.Get("User-Agent"), "ZTG-Scanner"):
			bad.Store("user agent " + r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "image/x-icon")
		w.Write(testICO())
	}))
	defer s.Close()
	info, err := fetch(t, s, false)
	if err != nil {
		t.Fatal(err)
	}
	if info.SHA256 != testICOSHA256 || info.MMH3 != testICOMMH3 {
		t.Fatalf("%+v", info)
	}
	if v := bad.Load(); v != nil {
		t.Fatal(v)
	}
	if requests.Load() != 1 {
		t.Fatalf("%d requests, want exactly 1", requests.Load())
	}
}

func TestFetchFaviconTLSSelfSigned(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG\r\n\x1a\n0123456789"))
	}))
	defer s.Close()
	info, err := fetch(t, s, true)
	if err != nil || info.Format != "png" {
		t.Fatalf("%+v %v", info, err)
	}
	// Speaking plain HTTP to a TLS port yields nothing.
	if info, err := fetch(t, s, false); err == nil {
		t.Fatalf("plain HTTP to a TLS port accepted: %+v", info)
	}
}

func TestFetchFaviconRefusals(t *testing.T) {
	var follow atomic.Int32
	cases := map[string]http.HandlerFunc{
		"not found": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"html catch-all": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>login</html>"))
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/favicon.ico" {
				follow.Add(1)
			}
			http.Redirect(w, r, "/icon.png", http.StatusFound)
		},
		"declared too large": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/x-icon")
			w.Header().Set("Content-Length", "70000")
			w.Write(testICO())
		},
		"chunked too large": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/x-icon")
			w.Write([]byte{0, 0, 1, 0})
			w.(http.Flusher).Flush()
			for i := 0; i < 20; i++ {
				w.Write(make([]byte, 8192))
				w.(http.Flusher).Flush()
			}
		},
		"empty": func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Content-Type", "image/x-icon") },
	}
	for name, h := range cases {
		s := httptest.NewServer(h)
		if info, err := fetch(t, s, false); err == nil {
			t.Errorf("%s: accepted %+v", name, info)
		}
		s.Close()
	}
	if follow.Load() != 0 {
		t.Fatal("a redirect was followed")
	}
}

func TestFetchFaviconNeverHashesAPartialBody(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		c, buf, _ := hj.Hijack()
		defer c.Close()
		buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: image/x-icon\r\nContent-Length: 200\r\n\r\n")
		buf.Write(testICO()[:50]) // promises 200 bytes, sends 50, then closes
		buf.Flush()
	}))
	defer s.Close()
	if info, err := fetch(t, s, false); err == nil {
		t.Fatalf("partial body hashed: %+v", info)
	}
}

func TestFetchFaviconTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(time.Second) }))
	defer s.Close()
	start := time.Now()
	_, err := FetchFavicon(context.Background(), loopback, portOf(t, hostPort(t, s)), false, 300*time.Millisecond)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("%v after %s", err, time.Since(start))
	}
}

func TestFaviconSanitizeSavedValue(t *testing.T) {
	f := &FaviconInfo{SHA256: "XYZ", ContentType: "image/\x00x", Format: strings.Repeat("f", 50), Size: -4}
	f.Sanitize()
	if f.SHA256 != "" || f.ContentType != "image/x" || len(f.Format) != 8 || f.Size != 0 {
		t.Fatalf("%+v", f)
	}
	ok := &FaviconInfo{SHA256: testICOSHA256, Size: 200}
	ok.Sanitize()
	if ok.SHA256 != testICOSHA256 {
		t.Fatal("valid hash dropped")
	}
}

func FuzzFingerprintFavicon(f *testing.F) {
	f.Add(testICO(), "image/x-icon")
	f.Add([]byte("<html>"), "text/html")
	f.Add([]byte{}, "")
	f.Fuzz(func(t *testing.T, b []byte, ct string) {
		info, err := fingerprintFavicon(b, ct)
		if err != nil {
			return
		}
		if len(info.SHA256) != 64 || info.Size != len(b) || info.ContentType != CleanText(info.ContentType, 100) {
			t.Fatalf("%+v", info)
		}
	})
}
