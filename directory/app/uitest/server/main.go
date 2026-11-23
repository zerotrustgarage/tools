// Command server is the ZTG Directory UI test server. It is never shipped: it
// serves the desktop frontend on 127.0.0.1 with the real console logic
// (console.API over the demo directory) behind a stand-in for the Wails
// binding, so uitest/run.mjs drives the same code the window binds.
//
//	go run ./uitest/server -frontend frontend [-setup]
//
// With -setup it starts at the first-run setup instead, and the domain it
// configures is the same demo directory reached through a stand-in opener, so
// the setup form runs through the real console.Configure. It prints "listening http://127.0.0.1:<port>/" and exits when its standard
// input closes, removing its temporary data folder.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/zerotrustgarage/tools/directory/console"
	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/demo"
)

const shimPath = "/uitest-bindings.js"

func main() {
	frontend := flag.String("frontend", "frontend", "folder with the desktop frontend")
	setup := flag.Bool("setup", false, "start at the first-run setup")
	flag.Parse()
	data, err := os.MkdirTemp("", "ztg-directory-uitest-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(data)
	var backend console.Backend = console.NewDemoBackend()
	var wiring *console.LiveWiring
	if *setup {
		backend = console.SetupBackend{}
		dir := demo.NewDirectory()
		wiring = &console.LiveWiring{SettingsPath: filepath.Join(data, "settings.json"), Open: func(_ context.Context, c ad.Config) (ad.Writer, error) {
			if c.Credential == nil || string(c.Credential.Password) != console.DemoPassword {
				return nil, ad.ErrInvalidCredentials
			}
			dir.Account = c.Credential.Account()
			return dir, nil
		}}
	}
	svc, err := console.New(backend, console.Config{DataDir: data, Device: "uitest", Version: "0.0.0-uitest"})
	if err != nil {
		log.Fatal(err)
	}
	if err := svc.SeedTemplates(demo.Templates()); err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	h := newHandler(console.NewAPI(svc, wiring), *frontend, ln.Addr().String())
	go func() {
		if err := http.Serve(ln, h); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Print(err)
		}
	}()
	fmt.Printf("listening http://%s/\n", ln.Addr())
	_, _ = io.Copy(io.Discard, os.Stdin) // the parent closes stdin to stop the server
	ln.Close()
}

type handler struct {
	api      reflect.Value
	methods  []string
	frontend string
	host     string
}

func newHandler(api *console.API, frontend, host string) *handler {
	v := reflect.ValueOf(api)
	h := &handler{api: v, frontend: frontend, host: host}
	for i := 0; i < v.Type().NumMethod(); i++ {
		h.methods = append(h.methods, v.Type().Method(i).Name)
	}
	sort.Strings(h.methods)
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only this address may be used (no DNS rebinding through another name).
	if r.Host != h.host {
		http.Error(w, "wrong host", http.StatusMisdirectedRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/"):
		h.call(w, r, strings.TrimPrefix(r.URL.Path, "/api/"))
	case r.URL.Path == shimPath:
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(w, h.shim())
	default:
		h.file(w, r)
	}
}

// shim defines window.go.console.API like the Wails runtime does: each
// method returns a Promise that resolves with the result or rejects with an
// Error carrying the Go error text.
func (h *handler) shim() string {
	names, _ := json.Marshal(h.methods)
	return `// UI test stand-in for the Wails binding (served by uitest/server only).
(() => {
  const call = async (name, args) => {
    const res = await fetch("/api/" + name, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(args) });
    const out = await res.json();
    if (Object.prototype.hasOwnProperty.call(out, "error")) throw new Error(out.error);
    return out.result;
  };
  const api = {};
  for (const name of ` + string(names) + `) api[name] = (...args) => call(name, args);
  window.go = { console: { API: api } };
})();
`
}

func (h *handler) call(w http.ResponseWriter, r *http.Request, name string) {
	w.Header().Set("Content-Type", "application/json")
	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		reply(http.StatusMethodNotAllowed, map[string]string{"error": "POST JSON only"})
		return
	}
	m := h.api.MethodByName(name)
	if !m.IsValid() || name == "" {
		reply(http.StatusNotFound, map[string]string{"error": "unknown method " + name})
		return
	}
	var raw []json.RawMessage
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err == nil {
		err = json.Unmarshal(body, &raw)
	}
	if err != nil {
		reply(http.StatusBadRequest, map[string]string{"error": "arguments must be a JSON array"})
		return
	}
	t := m.Type()
	if len(raw) != t.NumIn() {
		reply(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%s takes %d arguments, got %d", name, t.NumIn(), len(raw))})
		return
	}
	args := make([]reflect.Value, len(raw))
	for i, a := range raw {
		p := reflect.New(t.In(i))
		dec := json.NewDecoder(bytes.NewReader(a))
		if err := dec.Decode(p.Interface()); err != nil {
			reply(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("argument %d: %v", i+1, err)})
			return
		}
		args[i] = p.Elem()
	}
	out := m.Call(args)
	errType := reflect.TypeOf((*error)(nil)).Elem()
	if n := len(out); n > 0 && t.Out(n-1) == errType {
		if e := out[n-1]; !e.IsNil() {
			reply(http.StatusOK, map[string]string{"error": e.Interface().(error).Error()})
			return
		}
		out = out[:n-1]
	}
	var result any
	if len(out) > 0 {
		result = out[0].Interface()
	}
	reply(http.StatusOK, map[string]any{"result": result})
}

var types = map[string]string{".html": "text/html; charset=utf-8", ".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8",
	".png": "image/png", ".svg": "image/svg+xml"}

func (h *handler) file(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	ct, ok := types[filepath.Ext(name)]
	if !ok || strings.HasPrefix(name, "wails/") {
		http.NotFound(w, r) // /wails/*.js: the shim stands in for them
		return
	}
	body, err := os.ReadFile(filepath.Join(h.frontend, filepath.FromSlash(name)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if name == "index.html" {
		body = rewriteIndex(body)
	}
	w.Header().Set("Content-Type", ct)
	w.Write(body)
}

// rewriteIndex swaps the Wails runtime scripts for the binding shim.
func rewriteIndex(b []byte) []byte {
	s := string(b)
	s = strings.Replace(s, `<script src="/wails/ipc.js"></script>`, "", 1)
	s = strings.Replace(s, `<script src="/wails/runtime.js"></script>`, `<script src="`+shimPath+`"></script>`, 1)
	return []byte(s)
}
