//go:build windows

package dirsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

func TestPowerShellCommandLine(t *testing.T) {
	for _, script := range []string{HealthScript, SyncScript} {
		cmd, err := PowerShellRunner{}.command(context.Background(), script)
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(cmd.Path) || !strings.EqualFold(filepath.Base(cmd.Path), "powershell.exe") ||
			!strings.HasSuffix(strings.ToLower(cmd.Path), `\system32\windowspowershell\v1.0\powershell.exe`) {
			t.Fatalf("path %q", cmd.Path)
		}
		args := cmd.Args[1:]
		want := []string{"-NoProfile", "-NonInteractive", "-NoLogo", "-ExecutionPolicy", "Bypass", "-EncodedCommand"}
		if len(args) != len(want)+1 {
			t.Fatalf("args %q", args)
		}
		for i, w := range want {
			if args[i] != w {
				t.Fatalf("arg %d = %q, want %q", i, args[i], w)
			}
		}
		got, err := decodeCommand(args[len(want)])
		if err != nil || got != script {
			t.Fatalf("encoded command does not decode to the fixed script: %v", err)
		}
		if len(strings.Join(cmd.Args, " ")) > 32000 {
			t.Fatal("command line too long for CreateProcess")
		}
	}
}

func TestPowerShellNoSecretOnCommandLine(t *testing.T) {
	const secret = "Sup3r-Secret-Pw!"
	cred := &ad.Credential{User: "a-alex", Domain: "EXAMPLE", Password: []byte(secret)}
	input, err := buildInput("sync01.example.com", opHealth, cred)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := PowerShellRunner{}.command(context.Background(), HealthScript)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range cmd.Args {
		if strings.Contains(a, secret) || strings.Contains(a, "sync01") || strings.Contains(a, "a-alex") {
			t.Fatalf("secret or directory value in argument %q", a)
		}
	}
	for _, e := range append(cmd.Env, os.Environ()...) {
		if strings.Contains(e, secret) {
			t.Fatal("secret in the environment")
		}
	}
	if !strings.Contains(string(input), secret) {
		t.Fatal("input does not carry the password")
	}
}

// TestPowerShellRunnerStdin runs Windows PowerShell with a fixed script that
// reads the JSON input from standard input the way the sync scripts do.
func TestPowerShellRunnerStdin(t *testing.T) {
	path, err := powerShellPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("Windows PowerShell is not installed")
	}
	const echo = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding -ArgumentList $false
$in = ConvertFrom-Json -InputObject ([Console]::In.ReadToEnd())
[Console]::Out.Write((ConvertTo-Json -InputObject @{ user = [string]$in.user; n = ([string]$in.password).Length } -Compress))
`
	cred := &ad.Credential{User: "Zoë", Password: []byte("pässword")}
	input, err := buildInput("sync01.example.com", opHealth, cred)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := PowerShellRunner{}.Run(ctx, echo, input)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got struct {
		User string `json:"user"`
		N    int    `json:"n"`
	}
	if err := decodeResult(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got.User != "Zoë" || got.N != 8 {
		t.Fatalf("got %+v", got)
	}

	// An error result comes back with a non-zero exit and is still readable.
	out, err = PowerShellRunner{}.Run(ctx, `[Console]::Out.Write('{"error":"Sync is already running"}'); exit 1`, nil)
	if err == nil || !strings.Contains(string(out), "already running") {
		t.Fatalf("error exit: %v %s", err, out)
	}

	// Output beyond the limit is refused.
	if _, err := (PowerShellRunner{MaxOutput: 16}).Run(ctx, `[Console]::Out.Write('x' * 100)`, nil); err == nil {
		t.Fatal("oversized output accepted")
	}
}
