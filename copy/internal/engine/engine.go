// Package engine launches the inbox Robocopy with direct arguments and containment.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"
)

type Engine struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Result struct {
	Arguments  []string  `json:"arguments"`
	Started    time.Time `json:"started"`
	ElapsedMS  int64     `json:"elapsed_ms"`
	NativeExit uint32    `json:"native_exit"`
	Canceled   bool      `json:"canceled"`
	InJob      bool      `json:"in_job_before_resume"`
}

func Discover() (Engine, error) {
	path, err := systemEngine()
	if err != nil {
		return Engine{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return Engine{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return Engine{}, err
	}
	return Engine{Path: path, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// IOCounters are the process I/O counters of the running engine
// (GetProcessIoCounters). They are activity, not completed bytes: RC0 measured
// reads equal to the data copied, writes that include the log, and no promise for
// offloaded or cloned copies.
type IOCounters struct {
	ReadOperations  uint64
	WriteOperations uint64
	OtherOperations uint64
	ReadBytes       uint64
	WriteBytes      uint64
	OtherBytes      uint64
}

// SampleInterval is how often a running engine is sampled (design section 6).
const SampleInterval = 500 * time.Millisecond

// Observer receives live readings while the engine runs. A zero Observer is valid.
type Observer struct {
	// IO is called about every SampleInterval, and once more after the process
	// exits, from the goroutine that waits for the process. It must not block.
	IO func(IOCounters)
}

func (e Engine) Run(ctx context.Context, args []string, stdout, stderr string) (Result, error) {
	return launch(ctx, e.Path, args, stdout, stderr, Observer{})
}

// RunObserved is Run with live readings. On platforms without the counters the
// observer is never called.
func (e Engine) RunObserved(ctx context.Context, args []string, stdout, stderr string, obs Observer) (Result, error) {
	return launch(ctx, e.Path, args, stdout, stderr, obs)
}
