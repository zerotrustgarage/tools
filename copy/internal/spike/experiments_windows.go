//go:build windows

package spike

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
)

type experimentRunner func(string, ...string) (capture, error)

type watchedFile struct {
	Path string
	Size int64
	Tail []byte
}

func (w watchedFile) snapshot() map[string]any {
	state := map[string]any{"kind": "destination", "path": w.Path, "expected_size": w.Size, "tail_matches": false}
	file, err := os.Open(w.Path)
	if err != nil {
		state["error"] = err.Error()
		return state
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		state["error"] = err.Error()
		return state
	}
	state["size"] = info.Size()
	data := make([]byte, len(w.Tail))
	if _, err := file.ReadAt(data, w.Size-int64(len(data))); err == nil {
		state["tail_matches"] = bytes.Equal(data, w.Tail)
	}
	// Allocation size and a matching tail do not prove all bytes completed.
	return state
}

// newFixtureJunction creates an NTFS mount-point junction without a shell or
// symlink privilege. Its only callers pass paths in the generated fixture root.
func newFixtureJunction(link, target string) error {
	if err := os.Mkdir(link, 0700); err != nil {
		return err
	}
	substitute, err := syscall.UTF16FromString("\\??\\" + target)
	if err != nil {
		return err
	}
	printName, err := syscall.UTF16FromString(target)
	if err != nil {
		return err
	}
	buffer := make([]byte, 16)
	binary.LittleEndian.PutUint32(buffer, 0xa0000003)
	binary.LittleEndian.PutUint16(buffer[8:], 0)
	binary.LittleEndian.PutUint16(buffer[10:], uint16((len(substitute)-1)*2))
	binary.LittleEndian.PutUint16(buffer[12:], uint16(len(substitute)*2))
	binary.LittleEndian.PutUint16(buffer[14:], uint16((len(printName)-1)*2))
	for _, unit := range append(substitute, printName...) {
		buffer = binary.LittleEndian.AppendUint16(buffer, unit)
	}
	binary.LittleEndian.PutUint16(buffer[4:], uint16(len(buffer)-8))
	path, err := syscall.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(path, syscall.GENERIC_WRITE, 7, nil, syscall.OPEN_EXISTING, 0x02200000, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	var returned uint32
	return syscall.DeviceIoControl(handle, 0x000900a4, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
}

func readNativeSummary(path string) (fixtureCounts, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fixtureCounts{}, err
	}
	text, err := DecodeUnicode(raw)
	if err != nil {
		return fixtureCounts{}, err
	}
	return fixtureSummary(text)
}

func runLocalExperiments(ctx context.Context, engine, root, output string, execute experimentRunner, m *manifest) error {
	run := func(name, source, destination string, options ...string) (capture, error) {
		args := []string{source, destination, "/R:0", "/W:0", "/BYTES", "/FP", "/UNICODE", "/UNILOG:" + filepath.Join(output, name+".native.log"), "/TEE"}
		return execute(name, append(args, options...)...)
	}
	mkdir := func(name string) (string, error) {
		path := filepath.Join(root, name)
		return path, os.MkdirAll(path, 0700)
	}
	source, err := mkdir("exit source")
	if err != nil {
		return err
	}
	destination := filepath.Join(root, "exit destination")
	if err := os.WriteFile(filepath.Join(source, "one.txt"), []byte("one"), 0600); err != nil {
		return err
	}
	first, err := run("exit-copied", source, destination, "/E", "/XJ")
	if err != nil {
		return err
	}
	m.Checks["copied-exit1"] = first.NativeExit == 1
	converged, err := run("exit-converged", source, destination, "/E", "/XJ")
	if err != nil {
		return err
	}
	m.Checks["converged-exit0"] = converged.NativeExit == 0
	if err := os.WriteFile(filepath.Join(destination, "extra.txt"), []byte("extra"), 0600); err != nil {
		return err
	}
	extra, err := run("exit-extra", source, destination, "/E", "/XJ")
	if err != nil {
		return err
	}
	m.Checks["extra-exit2"] = extra.NativeExit == 2
	if err := os.WriteFile(filepath.Join(source, "two.txt"), []byte("two"), 0600); err != nil {
		return err
	}
	combined, err := run("exit-copied-extra", source, destination, "/E", "/XJ")
	if err != nil {
		return err
	}
	m.Checks["copied-extra-exit3"] = combined.NativeExit == 3

	// A source file colliding with a destination directory reproduces mismatch
	// classification without deleting anything; /L makes this a preview.
	mismatchSource, err := mkdir("mismatch source")
	if err != nil {
		return err
	}
	mismatchDest, err := mkdir(filepath.Join("mismatch destination", "collision"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(mismatchSource, "collision"), []byte("file"), 0600); err != nil {
		return err
	}
	mismatch, err := run("exit-mismatch", mismatchSource, filepath.Dir(mismatchDest), "/E", "/XJ", "/L")
	if err != nil {
		return err
	}
	m.Notes = append(m.Notes, fmt.Sprintf("File/directory collision preview: native exit %d, mismatch bit present: %t", mismatch.NativeExit, mismatch.NativeExit&4 != 0))

	lockedSource, err := mkdir("locked source")
	if err != nil {
		return err
	}
	lockedPath := filepath.Join(lockedSource, "locked.txt")
	if err := os.WriteFile(lockedPath, []byte("locked fixture"), 0600); err != nil {
		return err
	}
	if err := func() error {
		path, err := syscall.UTF16PtrFromString(lockedPath)
		if err != nil {
			return err
		}
		handle, err := syscall.CreateFile(path, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			return err
		}
		defer syscall.CloseHandle(handle)
		locked, err := run("exit-locked", lockedSource, filepath.Join(root, "locked destination"), "/E", "/XJ")
		if err != nil {
			return err
		}
		m.Checks["locked-file-failure-bit8"] = locked.NativeExit&8 != 0 && locked.NormalizedExit == 2
		return nil
	}(); err != nil {
		return err
	}

	longSource := filepath.Join(root, "long source")
	relative := ""
	for i := 0; i < 9; i++ {
		relative = filepath.Join(relative, "segment-01234567890123456789012345")
	}
	if err := os.MkdirAll(filepath.Join(longSource, relative), 0700); err != nil {
		return err
	}
	const longName = "long-日本語-😀.txt"
	longData := []byte("long-path fixture")
	if err := os.WriteFile(filepath.Join(longSource, relative, longName), longData, 0600); err != nil {
		return err
	}
	longDest := filepath.Join(root, "long destination")
	result, err := run("long-tree", longSource, longDest, "/E", "/XJ")
	if err != nil {
		return err
	}
	actual, readErr := os.ReadFile(filepath.Join(longDest, relative, longName))
	m.Checks["long-tree-content"] = result.NativeExit < 8 && readErr == nil && bytes.Equal(actual, longData)
	extendedSource := "\\\\?\\" + filepath.Join(longSource, relative) + "\\"
	extendedDest := filepath.Join(root, "extended argument destination")
	result, err = run("long-extended-argument", extendedSource, extendedDest, "/E", "/XJ")
	if err != nil {
		return err
	}
	actual, readErr = os.ReadFile(filepath.Join(extendedDest, longName))
	m.Checks["extended-argument-content"] = result.NativeExit < 8 && readErr == nil && bytes.Equal(actual, longData)
	volumeRoot := filepath.VolumeName(root) + "\\"
	result, err = run("volume-root-quit", volumeRoot, filepath.Join(root, "root quit destination"), "/QUIT")
	if err != nil {
		return err
	}
	m.Checks["volume-root-quit-success"] = result.NativeExit < 8

	if err := junctionExperiments(root, run, m); err != nil {
		return err
	}
	preview, err := readNativeSummary(filepath.Join(output, "native-log-preview.native.log"))
	if err != nil {
		return fmt.Errorf("preview fixture summary: %w", err)
	}
	copied, err := readNativeSummary(filepath.Join(output, "native-log.native.log"))
	if err != nil {
		return fmt.Errorf("copy fixture summary: %w", err)
	}
	m.Checks["preview-copy-numeric-totals"] = preview == copied && copied[1][1] == 1 && copied[2][1] == 81920
	if err := writeJSON(filepath.Join(output, "preview-copy-totals.json"), map[string]any{"preview": preview, "copy": copied}); err != nil {
		return err
	}
	return timingExperiments(ctx, engine, root, output, m)
}

func junctionExperiments(root string, run func(string, string, string, ...string) (capture, error), m *manifest) error {
	source := filepath.Join(root, "purge empty source")
	if err := os.Mkdir(source, 0700); err != nil {
		return err
	}
	for _, variant := range []string{"control", "source-xj", "excluded"} {
		destination := filepath.Join(root, "purge destination "+variant)
		target := filepath.Join(root, "purge target "+variant)
		if err := os.MkdirAll(destination, 0700); err != nil {
			return err
		}
		if err := os.Mkdir(target, 0700); err != nil {
			return err
		}
		sentinel := filepath.Join(target, "sentinel.txt")
		if err := os.WriteFile(sentinel, []byte("private junction sentinel"), 0600); err != nil {
			return err
		}
		link := filepath.Join(destination, "junction")
		if err := newFixtureJunction(link, target); err != nil {
			return fmt.Errorf("create fixture junction: %w", err)
		}
		options := []string{"/E", "/PURGE"}
		if variant == "source-xj" {
			options = append(options, "/XJ")
		}
		if variant == "excluded" {
			options = append(options, "/XD", link)
		}
		result, err := run("purge-junction-"+variant, source, destination, options...)
		// Remove the junction explicitly before generic temporary-tree cleanup.
		_ = os.Remove(link)
		if err != nil {
			return err
		}
		data, readErr := os.ReadFile(sentinel)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		survived := readErr == nil && bytes.Equal(data, []byte("private junction sentinel"))
		m.Notes = append(m.Notes, fmt.Sprintf("Destination junction %s: sentinel survived %t; native exit %d", variant, survived, result.NativeExit))
		m.Checks["junction-"+variant+"-native-success"] = result.NativeExit < 8
		if variant == "excluded" {
			m.Checks["junction-xd-protects-target"] = survived
		}
	}
	return nil
}

func timingExperiments(ctx context.Context, engine, root, output string, m *manifest) error {
	if !slices.Contains(m.Options, "/IORATE") || !slices.Contains(m.Options, "/THRESHOLD") {
		m.Notes = append(m.Notes, "Controlled timing skipped: this build does not advertise IORATE and THRESHOLD.")
		return nil
	}
	source := filepath.Join(root, "timing source")
	if err := os.Mkdir(source, 0700); err != nil {
		return err
	}
	data := bytes.Repeat([]byte{0x5a}, 4*1024*1024)
	if err := os.WriteFile(filepath.Join(source, "slow.bin"), data, 0600); err != nil {
		return err
	}
	for _, mode := range []string{"serial", "mt8"} {
		name := "timing-" + mode
		destination := filepath.Join(root, name)
		args := []string{source, destination, "/E", "/XJ", "/R:0", "/W:0", "/BYTES", "/FP", "/UNICODE", "/IORATE:1m", "/THRESHOLD:1", "/NOOFFLOAD", "/UNILOG:" + filepath.Join(output, name+".native.log"), "/TEE"}
		if slices.Contains(m.Options, "/NOCLONE") {
			args = append(args, "/NOCLONE")
		}
		if mode == "mt8" {
			args = append(args, "/MT:8")
		}
		path := filepath.Join(destination, "slow.bin")
		watch := watchedFile{Path: path, Size: int64(len(data)), Tail: data[len(data)-64:]}
		result, err := runCase(ctx, engine, output, name, args, watch)
		m.Cases = append(m.Cases, result)
		if err != nil {
			return err
		}
		actual, readErr := os.ReadFile(path)
		m.Checks[name+"-content"] = result.NativeExit < 8 && readErr == nil && bytes.Equal(actual, data)
		timing, err := analyzeTiming(filepath.Join(output, name+".events.jsonl"))
		if err != nil {
			return err
		}
		timing["duration_ms"] = result.DurationMS
		if err := writeJSON(filepath.Join(output, name+".timing.json"), timing); err != nil {
			return err
		}
		m.Notes = append(m.Notes, fmt.Sprintf("%s: first filename at %v ms, first matching destination tail at %v ms; a matching tail is not proof of full completion", name, timing["first_filename_ms"], timing["first_tail_match_ms"]))
	}
	return nil
}

func analyzeTiming(path string) (map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	firstLine, firstTail := int64(-1), int64(-1)
	pipe := []byte{}
	wideName := []byte{}
	for _, char := range "slow.bin" {
		wideName = append(wideName, byte(char), 0)
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 65536), 1024*1024)
	for scanner.Scan() {
		var event map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, err
		}
		var kind string
		var elapsed int64
		if err := json.Unmarshal(event["kind"], &kind); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(event["elapsed_ms"], &elapsed); err != nil {
			return nil, err
		}
		if kind == "pipe" {
			var stream string
			if err := json.Unmarshal(event["stream"], &stream); err != nil {
				return nil, err
			}
			if stream == "stdout" {
				var data []byte
				if err := json.Unmarshal(event["data"], &data); err != nil {
					return nil, err
				}
				pipe = append(pipe, data...)
				if firstLine < 0 && (bytes.Contains(pipe, []byte("slow.bin")) || bytes.Contains(pipe, wideName)) {
					firstLine = elapsed
				}
			}
		}
		if kind == "destination" {
			var matches bool
			if err := json.Unmarshal(event["tail_matches"], &matches); err != nil {
				return nil, err
			}
			if matches && firstTail < 0 {
				firstTail = elapsed
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	timing := map[string]any{}
	timing["first_filename_ms"] = firstLine
	timing["first_tail_match_ms"] = firstTail
	timing["filename_before_matching_tail"] = firstLine >= 0 && firstTail > firstLine
	timing["matching_tail_proves_full_completion"] = false
	timing["sampling_interval_ms"] = 100
	return timing, nil
}
