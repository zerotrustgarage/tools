//go:build windows

package dirsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// PowerShellRunner runs a fixed script in Windows PowerShell 5.1, started by
// absolute path from the system directory:
//
//	powershell.exe -NoProfile -NonInteractive -NoLogo -ExecutionPolicy Bypass -EncodedCommand <script>
//
// The input (which holds the password) goes to standard input, which is then
// closed; nothing secret or taken from the directory is put on the command
// line or in the environment. The process is killed when ctx ends.
type PowerShellRunner struct {
	// MaxOutput bounds standard output in bytes; DefaultMaxOutput when zero.
	MaxOutput int
}

// DefaultMaxOutput bounds a script's standard output.
const DefaultMaxOutput = 1 << 20

// maxStderr bounds the standard error kept for an error message.
const maxStderr = 64 << 10

// createNoWindow is CREATE_NO_WINDOW.
const createNoWindow = 0x08000000

var (
	psOnce sync.Once
	psPath string
	psErr  error
)

// powerShellPath returns %SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe
// from GetSystemDirectoryW, never from PATH.
func powerShellPath() (string, error) {
	psOnce.Do(func() {
		// kernel32 is a KnownDLL; it reports the trusted system directory.
		proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemDirectoryW")
		buf := make([]uint16, syscall.MAX_PATH+1)
		n, _, callErr := proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n == 0 || n >= uintptr(len(buf)) {
			psErr = fmt.Errorf("GetSystemDirectoryW failed: %v", callErr)
			return
		}
		psPath = syscall.UTF16ToString(buf[:n]) + `\WindowsPowerShell\v1.0\powershell.exe`
	})
	return psPath, psErr
}

// powerShellArgs is the argument list after the program name.
func powerShellArgs(script string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-NoLogo", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", EncodeCommand(script)}
}

// command builds the process for script; input is attached by Run.
func (p PowerShellRunner) command(ctx context.Context, script string) (*exec.Cmd, error) {
	path, err := powerShellPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, powerShellArgs(script)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	cmd.WaitDelay = 5 * time.Second
	return cmd, nil
}

// Run starts PowerShell with script, writes input to its standard input and
// closes it, and returns standard output. A non-zero exit returns the output
// with an error, so the caller can read a {"error":...} result.
func (p PowerShellRunner) Run(ctx context.Context, script string, input []byte) ([]byte, error) {
	max := p.MaxOutput
	if max <= 0 {
		max = DefaultMaxOutput
	}
	cmd, err := p.command(ctx, script)
	if err != nil {
		return nil, err
	}
	stdout := &limitedBuffer{max: max}
	stderr := &limitedBuffer{max: maxStderr}
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if stdout.overflow {
		return nil, fmt.Errorf("the sync server script wrote more than %d bytes", max)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			msg := firstLine(stderr.buf.String())
			if msg == "" {
				return stdout.buf.Bytes(), fmt.Errorf("PowerShell exited with code %d", exit.ExitCode())
			}
			return stdout.buf.Bytes(), fmt.Errorf("PowerShell exited with code %d: %s", exit.ExitCode(), clean(msg, 500))
		}
		return nil, fmt.Errorf("start PowerShell: %w", err)
	}
	return stdout.buf.Bytes(), nil
}

// limitedBuffer keeps the first max bytes and drops the rest, so a noisy
// process never blocks on a full pipe.
type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.max - b.buf.Len()
	if len(p) > room {
		b.overflow = true
		if room > 0 {
			b.buf.Write(p[:room])
		}
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#< CLIXML") {
			return l
		}
	}
	return ""
}
