package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/directory/console"
)

func testHandler(t *testing.T) *handler {
	t.Helper()
	svc, err := console.New(console.NewDemoBackend(), console.Config{DataDir: t.TempDir(), Device: "test"})
	if err != nil {
		t.Fatal(err)
	}
	front := t.TempDir()
	index := `<script src="/wails/ipc.js"></script>` + "\n" + `<script src="/wails/runtime.js"></script><script type="module" src="app.js"></script>`
	if err := os.WriteFile(filepath.Join(front, "index.html"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	return newHandler(console.NewAPI(svc, nil), front, "example.test")
}

func do(t *testing.T, h http.Handler, method, path, host, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = host
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestDispatch(t *testing.T) {
	h := testHandler(t)
	if _, out := do(t, h, "POST", "/api/Status", "example.test", "[]"); out["result"].(map[string]any)["mode"] != "demo" {
		t.Fatalf("status: %v", out)
	}
	if w, _ := do(t, h, "POST", "/api/Nope", "example.test", "[]"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown method: %d", w.Code)
	}
	if w, _ := do(t, h, "POST", "/api/Status", "other.test", "[]"); w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("foreign host: %d", w.Code)
	}
	if w, _ := do(t, h, "POST", "/api/Lookup", "example.test", `["x"]`); w.Code != http.StatusBadRequest {
		t.Fatalf("argument count: %d", w.Code)
	}
	_, out := do(t, h, "POST", "/api/SignIn", "example.test", `[{"user":"ztg.tech","password":"wrong"}]`)
	if e, _ := out["error"].(string); !strings.Contains(e, "incorrect") {
		t.Fatalf("error mapping: %v", out)
	}
	_, out = do(t, h, "POST", "/api/SignIn", "example.test", `[{"user":"ztg.tech","password":"`+console.DemoPassword+`"}]`)
	if out["result"].(map[string]any)["signedIn"] != true {
		t.Fatalf("sign in: %v", out)
	}
}

func TestIndexRewriteAndShim(t *testing.T) {
	h := testHandler(t)
	w, _ := do(t, h, "GET", "/", "example.test", "")
	if body := w.Body.String(); strings.Contains(body, "/wails/") || !strings.Contains(body, shimPath) {
		t.Fatalf("index not rewritten: %s", body)
	}
	w, _ = do(t, h, "GET", shimPath, "example.test", "")
	if !strings.Contains(w.Body.String(), `"SignIn"`) || !strings.Contains(w.Body.String(), "window.go = { console: { API: api } }") {
		t.Fatalf("shim: %s", w.Body.String())
	}
	if w, _ = do(t, h, "GET", "/../main.go", "example.test", ""); w.Code != http.StatusNotFound {
		t.Fatalf("path escape: %d", w.Code)
	}
}
