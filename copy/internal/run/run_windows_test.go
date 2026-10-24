package run

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/parse"
)

func TestPreviewCopyConvergenceAndRecord(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "source space"), filepath.Join(root, "destination")
	for _, p := range []string{src, dst} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	payload := []byte("café-日本語-😀 copy evidence")
	name := "café-日本語-😀.txt"
	if err := os.WriteFile(filepath.Join(src, name), payload, 0600); err != nil {
		t.Fatal(err)
	}
	j := job.Job{Schema: 1, Source: src, Destination: dst, Options: []string{"/MT:8", "/R:0", "/W:0"}}
	records := filepath.Join(root, "records")
	ctx := context.Background()
	plan, err := Execute(ctx, j, records, "test", false, io.Discard)
	if err != nil || plan.State != "planned" || plan.NormalizedExit != 0 {
		t.Fatalf("plan: %+v, %v", plan, err)
	}
	if _, err := os.Stat(filepath.Join(dst, name)); !os.IsNotExist(err) {
		t.Fatal("preview changed the destination")
	}
	record, err := Execute(ctx, j, records, "test", true, io.Discard)
	if err != nil || record.State != "copied" || record.NormalizedExit != 0 {
		for _, phase := range record.Phases {
			t.Logf("phase %s: %+v", phase.Name, phase.Summary)
		}
		t.Fatalf("run: %+v, %v", record, err)
	}
	data, err := os.ReadFile(filepath.Join(dst, name))
	if err != nil || string(data) != string(payload) {
		t.Fatalf("content %q, %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(record.Directory, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Record
	if err := json.Unmarshal(data, &saved); err != nil || saved.State != "copied" || len(saved.Phases) != 5 {
		t.Fatalf("record: %s, %v", data, err)
	}
	if saved.Probe == nil || saved.Probe.FileVersion.Value == "" || saved.Probe.Signature.Source == "" || len(saved.Probe.Privileges.Items) != 3 || saved.Probe.Elevation.Elevated == nil {
		t.Fatalf("probe metadata missing from record: %+v", saved.Probe)
	}
	for _, p := range saved.Phases {
		if !p.Process.InJob || p.Process.Canceled {
			t.Fatalf("uncontained phase: %+v", p)
		}
		if p.Name == "copy" && (p.Summary == nil || p.Summary.Bytes.Copied != uint64(len(payload)) || p.LogSHA256 == "") {
			t.Fatalf("copy evidence: %+v", p)
		}
	}
	j.Destination = src
	if _, err := Execute(ctx, j, records, "test", true, io.Discard); err == nil {
		t.Fatal("allowed overlapping trees")
	}
	j.Destination = dst
	if _, err := Execute(ctx, j, filepath.Join(src, "records"), "test", true, io.Discard); err == nil {
		t.Fatal("allowed records inside source")
	}
	j.Options = []string{"/MIR"}
	if _, err := Execute(ctx, j, records, "test", true, io.Discard); err == nil {
		t.Fatal("enabled destructive runner prematurely")
	}
}

func TestLockedDestinationProducesSavedErrorCause(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "source"), filepath.Join(root, "destination")
	for _, dir := range []string{src, dst} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []struct{ path, content string }{
		{filepath.Join(src, "locked.txt"), "new source content"},
		{filepath.Join(dst, "locked.txt"), "old"},
	} {
		if err := os.WriteFile(file.path, []byte(file.content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	name, err := windows.UTF16PtrFromString(filepath.Join(dst, "locked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	record, err := Execute(ctx, job.Job{Schema: 1, Source: src, Destination: dst, Options: []string{"/E", "/R:0", "/W:0", "/XJ"}}, filepath.Join(root, "records"), "test", true, io.Discard)
	if err != nil || record.NormalizedExit != 2 || record.State != "failed" {
		t.Fatalf("%+v, %v", record, err)
	}
	found := false
	for _, phase := range record.Phases {
		if phase.Name == "copy" && phase.Diagnostics != nil {
			for _, group := range phase.Diagnostics.Errors {
				found = found || group.Code == 32 && group.Count > 0 && group.Cause == "File in use"
			}
		}
	}
	if !found {
		data, _ := os.ReadFile(filepath.Join(record.Directory, "copy.log"))
		text, _ := parse.Unicode(data)
		t.Log(text)
		t.Fatalf("missing locked-file cause: %+v", record.Phases)
	}
	data, err := os.ReadFile(filepath.Join(record.Directory, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Record
	if err := json.Unmarshal(data, &saved); err != nil || saved.Phases[3].Diagnostics == nil || len(saved.Phases[3].Diagnostics.Errors) == 0 {
		t.Fatalf("diagnostics not saved: %s, %v", data, err)
	}
}
