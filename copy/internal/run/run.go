// Package run orchestrates guarded preview, copy and convergence with local evidence.
package run

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/engine"
	"github.com/zerotrustgarage/tools/copy/internal/guard"
	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/parse"
	"github.com/zerotrustgarage/tools/copy/internal/private"
)

type Phase struct {
	Name      string         `json:"name"`
	Process   engine.Result  `json:"process"`
	Summary   *parse.Summary `json:"summary,omitempty"`
	LogSHA256 string         `json:"log_sha256,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type Record struct {
	Schema         int           `json:"schema"`
	Version        string        `json:"version"`
	Mode           string        `json:"mode"`
	Directory      string        `json:"directory"`
	Started        time.Time     `json:"started"`
	Finished       time.Time     `json:"finished"`
	Host           string        `json:"host"`
	Technician     string        `json:"technician"`
	TechnicianID   string        `json:"technician_id"`
	Job            job.Job       `json:"job"`
	Engine         engine.Engine `json:"engine"`
	Guards         guard.Facts   `json:"guards"`
	Phases         []Phase       `json:"phases"`
	State          string        `json:"state"`
	NormalizedExit int           `json:"normalized_exit"`
	Error          string        `json:"error,omitempty"`
}

func DefaultRecords() (string, error) {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		return "", errors.New("LOCALAPPDATA is unavailable; select an explicit --report directory")
	}
	return filepath.Join(dir, "ZTG", "Copy", "runs"), nil
}

func Execute(ctx context.Context, j job.Job, root, version string, copyData bool, progress io.Writer) (record Record, err error) {
	record = Record{Schema: 1, Version: version, Job: j, Started: time.Now().UTC(), State: "error", NormalizedExit: 3, Phases: []Phase{}, Mode: "plan"}
	if copyData {
		record.Mode = "run"
	}
	facts, err := guard.InspectContext(ctx, j, root)
	if err != nil {
		return record, err
	}
	record.Guards = facts
	if err := ctx.Err(); err != nil {
		return record, err
	}
	// No evidence is written into either copy tree.
	if err := private.EnsureDir(root); err != nil {
		return record, err
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return record, err
	}
	record.Directory = filepath.Join(root, time.Now().Format("2006-01-02"), hex.EncodeToString(random[:]))
	if err := private.EnsureDir(record.Directory); err != nil {
		return record, err
	}
	defer func() {
		record.Finished = time.Now().UTC()
		if err != nil {
			record.Error = err.Error()
			record.NormalizedExit = 3
			record.State = "error"
			if ctx.Err() != nil {
				record.State = "canceled"
			}
		}
		if writeErr := private.WriteNew(filepath.Join(record.Directory, "run.json"), record); writeErr != nil {
			err = errors.Join(err, fmt.Errorf("write run record: %w", writeErr))
			record.NormalizedExit = 3
			record.State = "error"
		}
	}()
	record.Host, err = os.Hostname()
	if err != nil {
		return record, err
	}
	technician, err := user.Current()
	if err != nil {
		return record, err
	}
	record.Technician, record.TechnicianID = technician.Username, technician.Uid
	record.Engine, err = engine.Discover()
	if err != nil {
		return record, err
	}
	fmt.Fprintln(progress, "Record:", record.Directory)
	events, err := os.OpenFile(filepath.Join(record.Directory, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return record, err
	}
	defer events.Close()
	emit := func(name, state string) error {
		if err := json.NewEncoder(events).Encode(map[string]any{"time": time.Now().UTC(), "phase": name, "state": state}); err != nil {
			return err
		}
		return events.Sync()
	}
	raw := func(name string, args []string) (engine.Result, error) {
		fmt.Fprintln(progress, name+"...")
		if err := emit(name, "started"); err != nil {
			return engine.Result{}, err
		}
		result, launchErr := record.Engine.Run(ctx, args, filepath.Join(record.Directory, name+".stdout.bin"), filepath.Join(record.Directory, name+".stderr.bin"))
		eventErr := emit(name, "finished")
		return result, errors.Join(launchErr, eventErr)
	}
	probeCtx, cancelProbe := context.WithTimeout(ctx, 30*time.Second)
	help, err := record.Engine.Run(probeCtx, []string{"/?"}, filepath.Join(record.Directory, "help.stdout.bin"), filepath.Join(record.Directory, "help.stderr.bin"))
	cancelProbe()
	record.Phases = append(record.Phases, Phase{Name: "help", Process: help})
	if err != nil {
		return record, err
	}
	helpFile, err := os.Open(filepath.Join(record.Directory, "help.stdout.bin"))
	if err != nil {
		return record, err
	}
	helpBytes, readErr := io.ReadAll(io.LimitReader(helpFile, 1<<20))
	helpFile.Close()
	if readErr != nil {
		return record, readErr
	}
	supported := parse.SupportedTokens(helpBytes)
	for _, arg := range j.Arguments()[2:] {
		if len(arg) > 0 && arg[0] == '/' && !supported[job.Key(arg)] {
			return record, fmt.Errorf("installed Robocopy does not advertise %s", job.Key(arg))
		}
	}
	validated, err := raw("validate", append(j.Arguments(), "/QUIT"))
	record.Phases = append(record.Phases, Phase{Name: "validate", Process: validated})
	if err != nil {
		return record, err
	}
	if parse.Exit(validated.NativeExit) != 0 {
		record.State, record.NormalizedExit = "invalid", parse.Exit(validated.NativeExit)
		return record, nil
	}
	var plannedBytes uint64
	phase := func(name string, list bool) (Phase, error) {
		p := Phase{Name: name}
		newFacts, err := guard.InspectContext(ctx, j, root)
		if err != nil {
			return p, err
		}
		if newFacts.SourceID != facts.SourceID || newFacts.DestinationID != facts.DestinationID || newFacts.SourceCanonical != facts.SourceCanonical || newFacts.DestinationCanonical != facts.DestinationCanonical {
			return p, errors.New("endpoint identity changed after probing")
		}
		if name == "copy" && plannedBytes > newFacts.FreeBytes {
			return p, errors.New("available destination space fell below the preview byte total")
		}
		jobArgs := j.Arguments()
		if name == "convergence" {
			// /MT directory "copied" totals count traversal, even with native 0.
			// Serial list-only verification retains selection/security semantics and
			// gives meaningful directory create/skip counts for convergence.
			serial := make([]string, 0, len(jobArgs))
			for _, arg := range jobArgs {
				if job.Key(arg) != "/MT" {
					serial = append(serial, arg)
				}
			}
			jobArgs = serial
		}
		args := append(jobArgs, "/BYTES", "/FP", "/X", "/TEE", "/UNILOG:"+filepath.Join(record.Directory, name+".log"))
		if list {
			args = append(args, "/L")
		}
		p.Process, err = raw(name, args)
		if err != nil {
			p.Error = err.Error()
			return p, err
		}
		summary, hash, err := readNative(filepath.Join(record.Directory, name+".log"))
		p.LogSHA256 = hash
		if err != nil {
			p.Error = err.Error()
			return p, err
		}
		p.Summary = &summary
		fmt.Fprintf(progress, "%s: files to copy/copied %d, bytes %d, extras %d, native exit %d\n", name, summary.Files.Copied, summary.Bytes.Copied, summary.Files.Extras, p.Process.NativeExit)
		return p, nil
	}
	preview, err := phase("preview", true)
	record.Phases = append(record.Phases, preview)
	if err != nil {
		return record, err
	}
	if code := parse.Exit(preview.Process.NativeExit); code != 0 {
		record.State, record.NormalizedExit = "preview-failed", code
		return record, nil
	}
	if !copyData {
		record.State, record.NormalizedExit = "planned", 0
		return record, nil
	}
	if preview.Summary == nil || preview.Summary.Bytes.Copied > facts.FreeBytes {
		return record, errors.New("insufficient available destination space for the preview byte total")
	}
	plannedBytes = preview.Summary.Bytes.Copied
	copied, err := phase("copy", false)
	record.Phases = append(record.Phases, copied)
	if err != nil {
		return record, err
	}
	record.NormalizedExit = parse.Exit(copied.Process.NativeExit)
	verification, err := phase("convergence", true)
	record.Phases = append(record.Phases, verification)
	if err != nil {
		return record, err
	}
	if code := parse.Exit(verification.Process.NativeExit); code > record.NormalizedExit {
		record.NormalizedExit = code
	}
	if verification.Summary == nil || !verification.Summary.Converged() {
		if record.NormalizedExit == 0 {
			record.NormalizedExit = 1
		}
	}
	record.State = "copied"
	if record.NormalizedExit == 1 {
		record.State = "review"
	} else if record.NormalizedExit > 1 {
		record.State = "failed"
	}
	return record, nil
}

// Read a bounded footer for correctness and stream the whole native log for its hash.
func readNative(name string) (parse.Summary, string, error) {
	f, err := os.Open(name)
	if err != nil {
		return parse.Summary{}, "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return parse.Summary{}, "", err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	info, err := f.Stat()
	if err != nil {
		return parse.Summary{}, hash, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return parse.Summary{}, hash, err
	}
	bom := [2]byte{}
	if _, err := io.ReadFull(f, bom[:]); err != nil || bom != [2]byte{0xff, 0xfe} || info.Size()%2 != 0 {
		return parse.Summary{}, hash, errors.New("native log is not complete UTF-16LE")
	}
	offset := int64(0)
	if info.Size() > 1<<20 {
		offset = (info.Size() - 1<<20) &^ 1
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return parse.Summary{}, hash, err
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+2))
	if err != nil {
		return parse.Summary{}, hash, err
	}
	if offset > 0 {
		// Discard an incomplete first line, preserving surrogate boundaries.
		found := false
		for i := 0; i+1 < len(data); i += 2 {
			if data[i] == 10 && data[i+1] == 0 {
				data = append([]byte{0xff, 0xfe}, data[i+2:]...)
				found = true
				break
			}
		}
		if !found {
			return parse.Summary{}, hash, errors.New("native log footer has no complete line")
		}
	}
	summary, err := parse.NativeSummary(data)
	return summary, hash, err
}
