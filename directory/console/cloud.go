package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/dirsync"
	"github.com/zerotrustgarage/tools/directory/engine/m365"
	"github.com/zerotrustgarage/tools/directory/engine/mirror"
	"github.com/zerotrustgarage/tools/directory/engine/record"
)

// CloudStatus is the Microsoft 365 side of the session as the window shows it.
type CloudStatus struct {
	// Configured is true when the tenant and app registration are set up.
	Configured bool   `json:"configured"`
	SignedIn   bool   `json:"signedIn"`
	Account    string `json:"account,omitempty"` // the signed-in cloud account (UPN)
	TenantID   string `json:"tenantId,omitempty"`
	Cloud      string `json:"cloud,omitempty"` // commercial, gcchigh or dod
	// SyncKind is "", "connect" (Entra Connect Sync) or "cloud" (Cloud Sync).
	SyncKind   string `json:"syncKind,omitempty"`
	SyncServer string `json:"syncServer,omitempty"`
	// SyncReady is true when Sync now and sync health can run in this build.
	SyncReady bool   `json:"syncReady"`
	Note      string `json:"note"`
}

// CloudBackend is the Microsoft 365 and directory synchronization source of a
// Backend. Every call runs as the signed-in technician; the cloud sign-in ends
// with the AD session.
type CloudBackend interface {
	CloudStatus() CloudStatus
	// CloudSignIn runs the delegated Microsoft 365 sign-in (design 6.2a). It
	// needs an unlocked AD session.
	CloudSignIn(ctx context.Context) error
	CloudSignOut()
	// Tenant returns the signed-in tenant, m365.ErrNotSignedIn when there is
	// none, or an error while the AD session is signed out or locked.
	Tenant() (m365.Tenant, error)
	// Syncer returns the configured sync method, dirsync.ErrNotConfigured
	// when there is none.
	Syncer() (dirsync.Syncer, error)
}

// SyncSettings name the customer's sync method.
type SyncSettings struct {
	Kind   dirsync.Kind `json:"kind,omitempty"`   // "", connect, cloud
	Server string       `json:"server,omitempty"` // the Connect Sync server's DNS name
}

// CloudSettings are saved in cloud.json beside settings.json (CloudSettingsPath),
// so the sign-in settings file keeps its format.
type CloudSettings struct {
	M365 *m365.Settings `json:"m365,omitempty"`
	Sync SyncSettings   `json:"sync"`
}

// Validate checks the cloud settings' shape.
func (c CloudSettings) Validate() error {
	if c.M365 != nil {
		if err := c.M365.Validate(); err != nil {
			return err
		}
	}
	switch c.Sync.Kind {
	case dirsync.KindNone:
	case dirsync.KindConnect:
		if strings.TrimSpace(c.Sync.Server) == "" {
			return errors.New("Connect Sync needs the sync server's DNS name")
		}
	case dirsync.KindCloud:
		if c.M365 == nil {
			return errors.New("Cloud Sync provisioning on demand needs the Microsoft 365 tenant and app registration")
		}
	default:
		return fmt.Errorf("unknown sync method %q (connect or cloud)", c.Sync.Kind)
	}
	return nil
}

// CloudSettingsPath is the cloud settings file beside the sign-in settings.
func CloudSettingsPath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "cloud.json")
}

const maxCloudSettings = 64 << 10

// LoadCloudSettings reads path; a missing file means no Microsoft 365 and no
// sync method.
func LoadCloudSettings(path string) (CloudSettings, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return CloudSettings{}, nil
	}
	if err != nil {
		return CloudSettings{}, fmt.Errorf("open cloud settings: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCloudSettings+1))
	if err != nil {
		return CloudSettings{}, fmt.Errorf("read cloud settings: %w", err)
	}
	if len(data) > maxCloudSettings {
		return CloudSettings{}, fmt.Errorf("cloud settings %s are larger than %d bytes", path, maxCloudSettings)
	}
	var c CloudSettings
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return CloudSettings{}, fmt.Errorf("cloud settings %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return CloudSettings{}, fmt.Errorf("cloud settings %s: %w", path, err)
	}
	return c, nil
}

// SaveCloudSettings writes c to path.
func SaveCloudSettings(path string, c CloudSettings) error {
	if err := c.Validate(); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(path, append(raw, '\n'), 0o640)
}

// CloudWiring is what the live backend needs from the app for the cloud side.
type CloudWiring struct {
	// OpenBrowser opens the Microsoft sign-in page; nil uses
	// m365.DefaultOpenBrowser.
	OpenBrowser func(url string) error
	// SyncRunner runs the fixed Connect Sync scripts through PowerShell
	// remoting; nil leaves Connect Sync unavailable in this build.
	SyncRunner dirsync.Runner
}

// cloudNote is the object page's line about the Microsoft 365 side.
func cloudNote(st CloudStatus, kind string) string {
	switch {
	case !st.Configured:
		return "Microsoft 365 is not connected to this console. The mirror pane (cloud sign-in state, licenses, sync status and Sync now) needs the tenant and app registration in the setup."
	case !st.SignedIn:
		return "Microsoft 365 is set up, but you are not signed in to it: sign in to Microsoft 365 to see this object's cloud side."
	case kind != "user":
		return "Signed in to Microsoft 365 as " + st.Account + ". The mirror pane covers users in this version; groups and contacts follow."
	}
	return "Signed in to Microsoft 365 as " + st.Account + ". The Microsoft 365 pane compares this user with its cloud user."
}

// CloudStatus reports the Microsoft 365 side.
func (s *Service) CloudStatus() CloudStatus { return s.backend().CloudStatus() }

// CloudSignIn signs the technician in to Microsoft 365 through the system
// browser and records the attempt.
func (s *Service) CloudSignIn() (CloudStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	b := s.backend()
	st := b.Status()
	err := b.CloudSignIn(ctx)
	cs := b.CloudStatus()
	if err != nil {
		s.log.Append(record.Entry{Kind: record.KindSignInFailed, Technician: st.Account, Device: s.cfg.Device, Method: "Microsoft 365 (delegated)",
			Note: "Microsoft 365 sign-in", Error: err.Error()})
		return cs, err
	}
	s.log.Append(record.Entry{Kind: record.KindSignIn, Requester: st.Account, Technician: st.Account, Actor: cs.Account, Device: s.cfg.Device,
		Method: "Microsoft 365 (delegated)", Note: "Microsoft 365 sign-in as " + cs.Account})
	return cs, nil
}

// CloudSignOut ends the Microsoft 365 sign-in and erases its tokens.
func (s *Service) CloudSignOut() CloudStatus {
	b := s.backend()
	if cs := b.CloudStatus(); cs.SignedIn {
		s.log.Append(record.Entry{Kind: record.KindSignOut, Technician: b.Status().Account, Actor: cs.Account, Device: s.cfg.Device, Note: "Microsoft 365 sign-out"})
	}
	b.CloudSignOut()
	return b.CloudStatus()
}

func (s *Service) tenant() (m365.Tenant, error) {
	t, err := s.backend().Tenant()
	if errors.Is(err, m365.ErrNotSignedIn) {
		return nil, errors.New("sign in to Microsoft 365 to see the cloud side")
	}
	return t, err
}

// Mirror builds the Microsoft 365 pane for dn. It is separate from Object so
// the object page stays fast.
func (s *Service) Mirror(dn string) (mirror.View, error) {
	w, err := s.writer()
	if err != nil {
		return mirror.View{}, err
	}
	t, err := s.tenant()
	if err != nil {
		return mirror.View{}, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	o, err := w.Get(ctx, dn)
	if err != nil {
		return mirror.View{}, err
	}
	return mirror.Mirror(ctx, t, o, mirror.WithManagerResolver(func(ctx context.Context, dn string) (string, error) {
		m, err := w.Get(ctx, dn)
		if err != nil {
			return "", err
		}
		return m.First("userprincipalname"), nil
	}))
}

func (s *Service) syncer() (dirsync.Syncer, error) {
	if _, err := s.writer(); err != nil {
		return nil, err
	}
	return s.backend().Syncer()
}

// SyncHealth reads the sync server's state (runbook R28).
func (s *Service) SyncHealth() (dirsync.Health, error) {
	sy, err := s.syncer()
	if err != nil {
		return dirsync.Health{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return sy.Health(ctx)
}

// SyncResponse is the outcome of Sync now. The pane then calls Mirror every
// PollSeconds until the cloud user's lastSync is later than LastSyncBefore,
// for at most WatchMinutes, and shows the differences or the sync error.
type SyncResponse struct {
	Kind    string `json:"kind"` // connect or cloud
	Started string `json:"started"`
	Message string `json:"message"`
	// LastSyncBefore is the cloud user's last sync time before Sync now (RFC
	// 3339), empty when no linked cloud user was found or Microsoft 365 is
	// not signed in.
	LastSyncBefore string `json:"lastSyncBefore,omitempty"`
	// Watch is true when the pane can watch the cloud user for the change.
	Watch        bool `json:"watch"`
	PollSeconds  int  `json:"pollSeconds"`
	WatchMinutes int  `json:"watchMinutes"`
}

// SyncNow starts a Connect Sync delta cycle, or provisions dn on demand with
// Cloud Sync, and records it.
func (s *Service) SyncNow(dn string) (SyncResponse, error) {
	w, err := s.writer()
	if err != nil {
		return SyncResponse{}, err
	}
	sy, err := s.backend().Syncer()
	if err != nil {
		return SyncResponse{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	o, err := w.Get(ctx, dn)
	if err != nil {
		return SyncResponse{}, err
	}
	out := SyncResponse{Kind: string(sy.Kind()), PollSeconds: 15, WatchMinutes: 10}
	if sy.Kind() == dirsync.KindCloud {
		out.PollSeconds, out.WatchMinutes = 5, 5
	}
	var cloudAccount string
	if t, terr := s.backend().Tenant(); terr == nil {
		cloudAccount = t.Account()
		if u, ferr := mirror.Find(ctx, t, o); ferr == nil && u != nil {
			out.Watch = true
			if u.OnPremisesLastSyncDateTime != nil {
				out.LastSyncBefore = u.OnPremisesLastSyncDateTime.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	st := s.Status()
	actor := st.Account
	if sy.Kind() == dirsync.KindCloud && cloudAccount != "" {
		actor = cloudAccount
	}
	entry := record.Entry{Kind: record.KindSync, Requester: st.Account, Technician: st.Account, Actor: actor, DC: st.DC, DN: o.DN,
		Device: s.cfg.Device, Method: string(sy.Kind())}
	res, err := sy.SyncNow(ctx, o)
	if err != nil {
		entry.Result, entry.Error, entry.Note = "failed", err.Error(), "Sync now"
		s.log.Append(entry)
		return SyncResponse{}, err
	}
	entry.Result, entry.Note = "started", "Sync now: "+res.Message
	if _, err := s.log.Append(entry); err != nil {
		return SyncResponse{}, fmt.Errorf("sync started, but the change record could not be written: %w", err)
	}
	out.Started, out.Message = res.Started.UTC().Format(time.RFC3339), res.Message
	return out, nil
}

// adSessionError checks the AD session a cloud call runs under.
func adSessionError(st Status) error {
	switch {
	case !st.SignedIn:
		return errors.New("sign in first")
	case st.Locked:
		return errors.New("the session is locked; enter your password to continue")
	}
	return nil
}
