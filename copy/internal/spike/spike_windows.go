//go:build windows

package spike

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var systemDirectory = kernel32.NewProc("GetSystemDirectoryW")
var getIOCounters = kernel32.NewProc("GetProcessIoCounters")

type counters struct {
	ReadOperations, WriteOperations, OtherOperations uint64
	ReadBytes, WriteBytes, OtherBytes                uint64
}

type capture struct {
	Name           string   `json:"name"`
	Arguments      []string `json:"arguments"`
	NativeExit     int      `json:"native_exit"`
	NormalizedExit int      `json:"normalized_exit"`
	DurationMS     int64    `json:"duration_ms"`
	UnicodeError   string   `json:"unicode_error,omitempty"`
}

type manifest struct {
	Schema     int             `json:"schema"`
	CapturedAt time.Time       `json:"captured_at"`
	Engine     string          `json:"engine"`
	SHA256     string          `json:"sha256"`
	Options    []string        `json:"options"`
	Cases      []capture       `json:"cases"`
	Checks     map[string]bool `json:"checks"`
	Notes      []string        `json:"notes"`
}

type observer struct {
	mu      sync.Mutex
	start   time.Time
	encoder *json.Encoder
	err     error
}

func (o *observer) event(value map[string]any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	value["elapsed_ms"] = time.Since(o.start).Milliseconds()
	if err := o.encoder.Encode(value); err != nil && o.err == nil {
		o.err = err
	}
}

type observedWriter struct {
	file   *os.File
	stream string
	events *observer
}

func (w observedWriter) Write(data []byte) (int, error) {
	n, err := w.file.Write(data)
	w.events.event(map[string]any{"kind": "pipe", "stream": w.stream, "bytes": n, "data": data[:n]})
	return n, err
}

func resolveEngine() (string, error) {
	buffer := make([]uint16, 32768)
	n, _, err := systemDirectory.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if n == 0 || n >= uintptr(len(buffer)) {
		return "", fmt.Errorf("GetSystemDirectoryW: %v", err)
	}
	return filepath.Join(syscall.UTF16ToString(buffer[:n]), "Robocopy.exe"), nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

// runCase captures exact pipe bytes and the time each chunk was received. These
// are observations, not completed-file events or trustworthy copy progress.
func runCase(ctx context.Context, engine, output, name string, args []string) (capture, error) {
	result := capture{Name: name, Arguments: args}
	stdout, err := os.OpenFile(filepath.Join(output, name+".stdout.bin"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(filepath.Join(output, name+".stderr.bin"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	defer stderr.Close()
	events, err := os.OpenFile(filepath.Join(output, name+".events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	defer events.Close()
	o := &observer{start: time.Now(), encoder: json.NewEncoder(events)}
	caseCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	command := exec.CommandContext(caseCtx, engine, args...)
	command.WaitDelay = 5 * time.Second
	command.Stdout = observedWriter{stdout, "stdout", o}
	command.Stderr = observedWriter{stderr, "stderr", o}
	if err := command.Start(); err != nil {
		return result, err
	}
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		handle, err := syscall.OpenProcess(0x0400, false, uint32(command.Process.Pid))
		if err != nil {
			o.event(map[string]any{"kind": "io_unavailable", "error": err.Error()})
			return
		}
		defer syscall.CloseHandle(handle)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			var count counters
			ok, _, callErr := getIOCounters.Call(uintptr(handle), uintptr(unsafe.Pointer(&count)))
			if ok != 0 {
				o.event(map[string]any{"kind": "io", "counters": count})
			} else {
				o.event(map[string]any{"kind": "io_unavailable", "error": callErr.Error()})
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	runErr := command.Wait()
	close(done)
	<-sampled
	result.DurationMS = time.Since(o.start).Milliseconds()
	if caseCtx.Err() != nil {
		return result, fmt.Errorf("%s: %w", name, caseCtx.Err())
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return result, runErr
	}
	result.NativeExit = command.ProcessState.ExitCode()
	result.NormalizedExit = NormalizedExit(result.NativeExit)
	if o.err != nil {
		return result, o.err
	}
	if err := stdout.Close(); err != nil {
		return result, err
	}
	raw, err := os.ReadFile(stdout.Name())
	if err != nil {
		return result, err
	}
	for _, arg := range args {
		if strings.EqualFold(arg, "/UNICODE") {
			decoded, decodeErr := DecodeUnicode(raw)
			if decodeErr != nil {
				result.UnicodeError = decodeErr.Error()
			} else if err := os.WriteFile(filepath.Join(output, name+".utf16-attempt.txt"), []byte(decoded), 0600); err != nil {
				return result, err
			}
		}
	}
	return result, writeJSON(filepath.Join(output, name+".json"), result)
}

// Run never accepts user source/destination paths. All copies and purges are
// confined to disposable, generated fixture directories under one temporary root.
func Run(ctx context.Context, output string) error {
	engine, err := resolveEngine()
	if err != nil {
		return err
	}
	// Refuse to overwrite any prior evidence, including an empty folder.
	if err := os.Mkdir(output, 0700); err != nil {
		return err
	}
	engineFile, err := os.Open(engine)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, engineFile)
	closeErr := engineFile.Close()
	if hashErr != nil || closeErr != nil {
		return errors.Join(hashErr, closeErr)
	}
	m := manifest{Schema: 1, CapturedAt: time.Now().UTC(), Engine: engine,
		SHA256: hex.EncodeToString(hash.Sum(nil)), Checks: map[string]bool{},
		Notes: []string{
			"Synthetic local fixtures only. No SMB, credentials, offload or block-clone conclusions.",
			"Pipe events mark receipt, not file completion. I/O counters are not copied-byte totals.",
			"Windows release, locale, file version and signature are captured by collect.ps1.",
		}}
	defer func() { _ = writeJSON(filepath.Join(output, "manifest.json"), m) }()
	root, err := os.MkdirTemp("", "ztg-copy-rc0-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	source := filepath.Join(root, "source with spaces")
	if err := os.MkdirAll(source, 0700); err != nil {
		return err
	}
	name := "café-日本語-😀.txt"
	payload := bytes.Repeat([]byte("synthetic RC0 data\r\n"), 4096)
	if err := os.WriteFile(filepath.Join(source, name), payload, 0600); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(source, "empty"), 0700); err != nil {
		return err
	}
	execute := func(name string, args ...string) (capture, error) {
		result, err := runCase(ctx, engine, output, name, args)
		m.Cases = append(m.Cases, result)
		return result, err
	}
	if _, err := execute("help-default", "/?"); err != nil {
		return err
	}
	if _, err := execute("help-unicode", "/UNICODE", "/?"); err != nil {
		return err
	}
	if help, err := os.ReadFile(filepath.Join(output, "help-default.stdout.bin")); err == nil {
		m.Options = HelpOptions(string(help))
	}
	for _, mode := range []string{"serial", "mt8", "trailing-slash", "native-log"} {
		destination := filepath.Join(root, "destination "+mode)
		srcArg, dstArg := source, destination
		if mode == "trailing-slash" {
			srcArg += "\\"
			dstArg += "\\"
		}
		args := []string{srcArg, dstArg, "/E", "/COPY:DAT", "/DCOPY:DAT", "/XJ", "/R:0", "/W:0", "/BYTES", "/FP", "/UNICODE"}
		if mode == "native-log" {
			args = append(args, "/UNILOG:"+filepath.Join(output, "native-log.native.log"), "/TEE")
		}
		if mode == "mt8" {
			args = append(args, "/MT:8")
		}
		preview, err := execute(mode+"-preview", append(append([]string{}, args...), "/L", "/X")...)
		if err != nil {
			return err
		}
		_, statErr := os.Stat(filepath.Join(destination, name))
		m.Checks[mode+"-preview-no-copy"] = os.IsNotExist(statErr)
		result, err := execute(mode+"-copy", args...)
		if err != nil {
			return err
		}
		actual, err := os.ReadFile(filepath.Join(destination, name))
		m.Checks[mode+"-copy-content"] = err == nil && bytes.Equal(actual, payload)
		m.Checks[mode+"-native-success"] = result.NativeExit < 8 && preview.NativeExit < 8
		raw, _ := os.ReadFile(filepath.Join(output, mode+"-copy.stdout.bin"))
		decoded, err := DecodeUnicode(raw)
		m.Notes = append(m.Notes, fmt.Sprintf("%s /UNICODE pipe decoded as pure UTF-16LE preserves the fixture name: %t", mode, err == nil && strings.Contains(decoded, name)))
		if mode == "native-log" {
			nativeRaw, readErr := os.ReadFile(filepath.Join(output, "native-log.native.log"))
			nativeText, decodeErr := DecodeUnicode(nativeRaw)
			m.Checks["native-log-unicode-name"] = readErr == nil && decodeErr == nil && strings.Contains(nativeText, name)
		}
	}
	valid, err := execute("quit-valid", source, filepath.Join(root, "quit destination"), "/E", "/QUIT", "/UNICODE")
	if err != nil {
		return err
	}
	invalid, err := execute("quit-invalid", source, filepath.Join(root, "quit destination"), "/ZTG_INVALID_OPTION", "/QUIT", "/UNICODE")
	if err != nil {
		return err
	}
	m.Checks["quit-valid-success"] = valid.NativeExit < 8
	m.Checks["quit-invalid-16"] = invalid.NativeExit == 16
	if err := writeJSON(filepath.Join(output, "manifest.json"), m); err != nil {
		return err
	}
	for name, passed := range m.Checks {
		if !passed {
			return fmt.Errorf("RC0 observation %q differs from the expected behavior; inspect manifest and raw captures", name)
		}
	}
	return nil
}
