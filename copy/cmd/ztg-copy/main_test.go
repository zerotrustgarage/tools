package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/copy/internal/job"
)

func TestDesignerWorkflow(t *testing.T) {
	var out, errOut bytes.Buffer
	ctx := context.Background()
	runCLI := func(args ...string) error {
		out.Reset()
		errOut.Reset()
		return run(ctx, args, &out, &errOut)
	}
	if err := runCLI("flags", "--search", "encryption", "--json"); err != nil || !strings.Contains(out.String(), "/COMPRESS") {
		t.Fatalf("%s %v", out.String(), err)
	}
	command := `robocopy "C:\source space" "\\nas\share" /MIR /FUTURE:9`
	if err := runCLI("explain", "--json", command); err != nil || !strings.Contains(out.String(), "not recognized") || !strings.Contains(out.String(), "destructive") {
		t.Fatalf("%s %v", out.String(), err)
	}
	name := filepath.Join(t.TempDir(), "job.json")
	if err := runCLI("import", "--output", name, command); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var j job.Job
	if err := json.Unmarshal(data, &j); err != nil || j.Options[1] != "/FUTURE:9" {
		t.Fatalf("%s %v", data, err)
	}
	if err := runCLI("import", "--output", name, command); err == nil {
		t.Fatal("import overwrote job")
	}
	if err := runCLI("validate", name); err == nil {
		t.Fatal("unknown option passed static validation")
	}
}

func TestTemplateImportRequiresAcceptance(t *testing.T) {
	// Set the platform-specific config selector; tests write only inside TempDir.
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	var out, errOut bytes.Buffer
	command := `robocopy C:\data D:\backup /E /R:2`
	if err := run(context.Background(), []string{"template", "save", "--command", command, "copy"}, &out, &errOut); err == nil {
		t.Fatal("saved imported command without acceptance")
	}
	if err := run(context.Background(), []string{"template", "save", "--accept", "--command", command, "copy"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(context.Background(), []string{"template", "command", "copy"}, &out, &errOut); err != nil || !strings.Contains(out.String(), "/W:5") {
		t.Fatalf("%s %v", out.String(), err)
	}
	if err := run(context.Background(), []string{"check-updates", "--help"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateRunUsesGuards(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	var out, errOut bytes.Buffer
	command := `robocopy C:\data D:\backup /MIR`
	if err := run(context.Background(), []string{"template", "save", "--accept", "--command", command, "mirror"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := run(context.Background(), []string{"template", "run", "--report", filepath.Join(dir, "records"), "mirror"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "/MIR is not enabled") {
		t.Fatalf("template bypassed execution policy: %v", err)
	}
}

func TestRCJImportExport(t *testing.T) {
	var out, errOut bytes.Buffer
	runCLI := func(args ...string) error {
		out.Reset()
		errOut.Reset()
		return run(context.Background(), args, &out, &errOut)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "in.rcj")
	text := "::\n\t/SD:C:\\data dir\t:: source\n\t/DD:D:\\backup\n\t/IF\n\t\t*.txt\n\t/S\n\t/MIR\n\t/FUTURE:1\n\t/REG\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runCLI("explain", "--rcj", file); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Source: C:\\data dir", "/FUTURE:1: UNKNOWN", "hidden option", "destructive", "not recognized"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("explanation lacks %q:\n%s", want, out.String())
		}
	}
	saved := filepath.Join(dir, "job.json")
	if err := runCLI("import", "--rcj", "--output", saved, file); err == nil || !strings.Contains(err.Error(), "--accept") {
		t.Fatalf("saved without acceptance: %v", err)
	}
	if _, err := os.Stat(saved); err == nil {
		t.Fatal("job file written before acceptance")
	}
	if err := runCLI("import", "--rcj", "--accept", "--output", saved, file); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(saved)
	var j job.Job
	if err := json.Unmarshal(data, &j); err != nil || j.Source != `C:\data dir` || len(j.Options) != 4 {
		t.Fatalf("%s %v", data, err)
	}
	if err := runCLI("import", "--rcj", "--accept", "--output", saved, file); err == nil {
		t.Fatal("import overwrote an existing job")
	}
	if err := runCLI("import", "--accept", "robocopy C:\\a D:\\b"); err == nil {
		t.Fatal("--accept without --rcj")
	}
	// The unsafe job cannot be exported; a clean one round trips through the CLI.
	if err := runCLI("export", "--rcj", saved, filepath.Join(dir, "bad.rcj")); err == nil {
		t.Fatal("exported a job with /REG and unknown options")
	}
	clean := filepath.Join(dir, "clean.json")
	if err := os.WriteFile(clean, []byte(`{"schema":1,"name":"nightly","source":"C:\\data dir","destination":"D:\\backup","files":["*.txt"],"options":["/S","/XD","cache dir"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(dir, "clean.rcj")
	if err := runCLI("export", "--rcj", clean, exported); err != nil {
		t.Fatal(err)
	}
	if err := runCLI("export", "--rcj", clean, exported); err == nil {
		t.Fatal("export overwrote a file")
	}
	if err := runCLI("export", "--rcj", "--encoding", "latin1", clean, filepath.Join(dir, "x.rcj")); err == nil {
		t.Fatal("accepted an unknown encoding")
	}
	if err := runCLI("export", clean, filepath.Join(dir, "y.rcj")); err == nil {
		t.Fatal("export without --rcj")
	}
	body, _ := os.ReadFile(exported)
	if !strings.Contains(string(body), "/SD:\"C:\\data dir\"") || strings.Contains(strings.ToUpper(string(body)), "/REG") {
		t.Fatalf("%s", body)
	}
	if err := runCLI("explain", "--json", "--rcj", exported); err != nil || !strings.Contains(out.String(), `"name": "nightly"`) {
		t.Fatalf("%s %v", out.String(), err)
	}
	if err := runCLI("explain", "--rcj", filepath.Join(dir, "missing.rcj")); err == nil {
		t.Fatal("read a missing file")
	}
	if err := runCLI("explain", "--rcj", dir); err == nil {
		t.Fatal("read a directory")
	}
}
