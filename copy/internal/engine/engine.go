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

func (e Engine) Run(ctx context.Context, args []string, stdout, stderr string) (Result, error) {
	return launch(ctx, e.Path, args, stdout, stderr)
}
