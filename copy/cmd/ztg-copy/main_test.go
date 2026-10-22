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
