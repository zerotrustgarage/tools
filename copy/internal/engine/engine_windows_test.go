package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
