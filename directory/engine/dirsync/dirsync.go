// Package dirsync drives directory synchronization to Microsoft 365 (design 6.4,
// 6.2a, runbook R3 and R28): Entra Connect Sync through PowerShell remoting to
// the sync server as the signed-in AD account, or Cloud Sync provisioning on
// demand through Microsoft Graph as the signed-in technician. This file is the
// contract; connect.go and cloud.go implement it and fake.go fakes it.
package dirsync

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// Kind of synchronization a customer uses.
type Kind string

const (
	KindNone    Kind = ""
	KindConnect Kind = "connect" // Microsoft Entra Connect Sync
	KindCloud   Kind = "cloud"   // Microsoft Entra Cloud Sync
)

// ErrNotConfigured reports that no sync method is configured.
var ErrNotConfigured = errors.New("directory synchronization is not configured")

// ErrBusy reports that a sync cycle is already running.
var ErrBusy = errors.New("a sync cycle is already running; try again when it finishes")

// Health is the sync server's state (runbook R28).
type Health struct {
	Kind             Kind       `json:"kind"`
	Server           string     `json:"server,omitempty"`
	Version          string     `json:"version,omitempty"`
	VersionStatus    string     `json:"versionStatus,omitempty"` // current, retiring, unsupported, unknown
	VersionNote      string     `json:"versionNote,omitempty"`
	StagingMode      bool       `json:"stagingMode"`
	SchedulerEnabled bool       `json:"schedulerEnabled"`
	IntervalMinutes  int        `json:"intervalMinutes,omitempty"`
	NextCycle        *time.Time `json:"nextCycle,omitempty"`
	LastCycle        *time.Time `json:"lastCycle,omitempty"`
	LastResult       string     `json:"lastResult,omitempty"`
	AppAuth          *bool      `json:"appAuth,omitempty"` // application-based authentication configured
	Warnings         []string   `json:"warnings,omitempty"`
	CheckedAt        time.Time  `json:"checkedAt"`
}

// Result of Sync now.
type Result struct {
	Kind    Kind      `json:"kind"`
	Started time.Time `json:"started"`
	Message string    `json:"message"`
}

// Syncer is a configured sync method.
type Syncer interface {
	Kind() Kind
	Health(ctx context.Context) (Health, error)
	// SyncNow starts a Connect Sync delta cycle (obj is only named in the
	// result), or provisions obj on demand with Cloud Sync.
	SyncNow(ctx context.Context, obj ad.Object) (Result, error)
}

// Fake is an in-memory Syncer for tests and demo mode. OnSync runs on each
// SyncNow (demo mode copies AD values to the fake tenant there).
type Fake struct {
	mu      sync.Mutex
	K       Kind
	H       Health
	OnSync  func(ctx context.Context, obj ad.Object) error
	Calls   []string
	Now     func() time.Time
	FailErr error
}

func (f *Fake) Kind() Kind { return f.K }

func (f *Fake) Health(ctx context.Context) (Health, error) {
	if err := ctx.Err(); err != nil {
		return Health{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	h := f.H
	h.Kind = f.K
	if f.Now != nil {
		h.CheckedAt = f.Now()
	}
	return h, nil
}

func (f *Fake) SyncNow(ctx context.Context, obj ad.Object) (Result, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, obj.DN)
	fail, on := f.FailErr, f.OnSync
	now := time.Now().UTC()
	if f.Now != nil {
		now = f.Now()
	}
	f.mu.Unlock()
	if fail != nil {
		return Result{}, fail
	}
	if on != nil {
		if err := on(ctx, obj); err != nil {
			return Result{}, err
		}
	}
	msg := "Delta sync cycle started"
	if f.K == KindCloud {
		msg = "Provisioned on demand"
	}
	f.mu.Lock()
	t := now
	f.H.LastCycle, f.H.LastResult = &t, "success"
	f.mu.Unlock()
	return Result{Kind: f.K, Started: now, Message: msg}, nil
}

var _ Syncer = (*Fake)(nil)
