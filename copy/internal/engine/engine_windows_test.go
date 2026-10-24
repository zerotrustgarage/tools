package engine

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/private"
)

func TestContainedRobocopyAndCancellation(t *testing.T) {
	e, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := private.EnsureDir(root); err != nil {
		t.Fatal(err)
	}
	src, dst := filepath.Join(root, "source space"), filepath.Join(root, "destination")
	for _, dir := range []string{src, dst} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	payload := []byte("Unicode fixture 日本語-😀")
	if err := os.WriteFile(filepath.Join(src, "café-日本語-😀.txt"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := e.Run(ctx, []string{src, dst, "/E", "/R:0", "/W:0", "/XJ"}, filepath.Join(root, "copy.stdout"), filepath.Join(root, "copy.stderr"))
	if err != nil || !r.InJob || r.Canceled || r.NativeExit != 1 {
		t.Fatalf("%+v, %v", r, err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "café-日本語-😀.txt"))
	if err != nil || string(got) != string(payload) {
		t.Fatalf("content: %q, %v", got, err)
	}
	stopCtx, stop := context.WithCancel(context.Background())
	timer := time.AfterFunc(500*time.Millisecond, stop)
	defer timer.Stop()
	start := time.Now()
	r, err = e.Run(stopCtx, []string{src, dst, "/MON:1", "/MOT:1", "/R:0", "/W:0"}, filepath.Join(root, "monitor.stdout"), filepath.Join(root, "monitor.stderr"))
	if !errors.Is(err, context.Canceled) || !r.Canceled || !r.InJob || time.Since(start) > 8*time.Second {
		t.Fatalf("stop: %+v, %v", r, err)
	}
}

func TestInspectRealRobocopyAndProcess(t *testing.T) {
	e, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	m := Inspect(e.Path)
	t.Logf("probe: version=%+v elevation=%+v elevated=%v privileges=%+v signature=%+v", m.FileVersion, m.Elevation, m.Elevation.Elevated != nil && *m.Elevation.Elevated, m.Privileges, m.Signature)
	osv := windows.RtlGetVersion()
	if want := fmt.Sprintf("%d.%d.", osv.MajorVersion, osv.MinorVersion); !strings.HasPrefix(m.FileVersion.Value, want) {
		t.Fatalf("file version %q does not start with the OS version %s (compatibility-shimmed read?)", m.FileVersion.Value, want)
	}
	if m.FileVersion.Value == "" || m.FileVersion.Unavailable != "" {
		t.Fatalf("version: %+v", m.FileVersion)
	}
	if m.Elevation.Unavailable != "" || m.Elevation.Elevated == nil || m.Elevation.Type == ElevationUnknown {
		t.Fatalf("elevation: %+v", m.Elevation)
	}
	if m.Privileges.Unavailable != "" || len(m.Privileges.Items) != len(RobocopyPrivileges) {
		t.Fatalf("privileges: %+v", m.Privileges)
	}
	for _, p := range m.Privileges.Items {
		if p.State != PrivilegeAbsent && p.State != PrivilegeDisabled && p.State != PrivilegeEnabled {
			t.Fatalf("privilege: %+v", p)
		}
	}
	if m.Elevation.Type == ElevationFull || *m.Elevation.Elevated {
		// An elevated admin token holds backup and restore privileges.
		for _, p := range m.Privileges.Items {
			if p.Name != "SeSecurityPrivilege" && p.State == PrivilegeAbsent {
				t.Fatalf("elevated token lacks %s", p.Name)
			}
		}
	}
	// Inbox Robocopy is Microsoft signed either inside the file or through a catalog.
	if m.Signature.Unavailable != "" || (m.Signature.Decision != SignatureEmbedded && m.Signature.Decision != SignatureCatalog) {
		t.Fatalf("robocopy signature: %+v", m.Signature)
	}
}

func TestInspectUnsignedFileIsUnsigned(t *testing.T) {
	exe, err := os.Executable() // the Go test binary is never signed or cataloged
	if err != nil {
		t.Fatal(err)
	}
	m := Inspect(exe)
	if m.Signature.Decision != SignatureUnsigned || m.Signature.Unavailable != "" {
		t.Fatalf("test binary signature: %+v", m.Signature)
	}
	if m.FileVersion.Value != "" && m.FileVersion.Unavailable != "" {
		t.Fatalf("version: %+v", m.FileVersion)
	}
}
