package console

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/auth"
	"github.com/zerotrustgarage/tools/directory/engine/dirsync"
	"github.com/zerotrustgarage/tools/directory/engine/m365"
)

// SetupBackend is used until the sign-in settings exist: it signs nobody in
// and tells the window to show the first-run setup.
type SetupBackend struct{}

func (SetupBackend) Status() Status {
	return Status{Mode: "setup", Hint: "Enter the domain this console manages and how people prove who they are. Settings are saved on this computer; no password is saved."}
}
func (SetupBackend) SignIn(context.Context, string, string, []byte, []byte) error {
	return errors.New("complete the setup first")
}
func (SetupBackend) SignOut() error { return nil }
func (SetupBackend) Lock()          {}
func (SetupBackend) Unlock(context.Context, []byte) error {
	return errors.New("complete the setup first")
}
func (SetupBackend) Touch()                     {}
func (SetupBackend) Writer() (ad.Writer, error) { return nil, errors.New("complete the setup first") }
func (SetupBackend) BaseDN() string             { return "" }
func (SetupBackend) CloudStatus() CloudStatus {
	return CloudStatus{Note: "Complete the setup first."}
}
func (SetupBackend) CloudSignIn(context.Context) error { return errors.New("complete the setup first") }
func (SetupBackend) CloudSignOut()                     {}
func (SetupBackend) Tenant() (m365.Tenant, error)      { return nil, errors.New("complete the setup first") }
func (SetupBackend) Syncer() (dirsync.Syncer, error) {
	return nil, errors.New("complete the setup first")
}

// SetupRequest is the first-run form.
type SetupRequest struct {
	Domain            string `json:"domain"`
	Server            string `json:"server"`
	BaseDN            string `json:"baseDN"`
	LDAPS             bool   `json:"ldaps"`
	AllowNTLM         bool   `json:"allowNTLM"`
	SecondFactor      string `json:"secondFactor"` // "", duo, radius
	AllowPasswordOnly bool   `json:"allowPasswordOnly"`
	RADIUSServer      string `json:"radiusServer"`
	RADIUSSecret      string `json:"radiusSecret"`
	UsernameFormat    string `json:"usernameFormat"`
	OperatorsGroup    string `json:"operatorsGroup"`
	ApproversGroup    string `json:"approversGroup"`
	IdleLockMinutes   int    `json:"idleLockMinutes"`
	Notice            string `json:"notice"`
	// Microsoft 365 (optional; saved in cloud.json beside settings.json).
	TenantID string `json:"tenantId"` // GUID or verified domain
	ClientID string `json:"clientId"` // the public client app registration
	Cloud    string `json:"cloud"`    // commercial (default), gcchigh or dod
	// SyncKind "cloud" also turns on Cloud Sync provisioning on demand.
	SyncKind   string `json:"syncKind"`   // "", connect or cloud
	SyncServer string `json:"syncServer"` // Connect Sync server DNS name
}

// cloudSettings turns the form's Microsoft 365 fields into CloudSettings;
// ok is false when none is filled in.
func (r SetupRequest) cloudSettings() (CloudSettings, bool) {
	var c CloudSettings
	tid, cid := strings.TrimSpace(r.TenantID), strings.TrimSpace(r.ClientID)
	if tid != "" || cid != "" {
		c.M365 = &m365.Settings{TenantID: tid, ClientID: cid, Cloud: m365.Cloud(strings.TrimSpace(r.Cloud)), CloudSync: r.SyncKind == string(dirsync.KindCloud)}
		if c.M365.Cloud == "" {
			c.M365.Cloud = m365.CloudCommercial
		}
	}
	c.Sync = SyncSettings{Kind: dirsync.Kind(strings.TrimSpace(r.SyncKind)), Server: strings.TrimSpace(r.SyncServer)}
	return c, c.M365 != nil || c.Sync.Kind != dirsync.KindNone
}

// Live wiring used by Configure; set by the desktop app (OpenWriter is
// Windows-only).
type LiveWiring struct {
	SettingsPath string
	Open         auth.Opener
	Protect      func([]byte) ([]byte, error)
	// Cloud wires the Microsoft 365 sign-in and Connect Sync.
	Cloud CloudWiring
}

var wiringMu sync.Mutex

// Configure saves sign-in settings (and a DPAPI-protected RADIUS secret), the
// optional Microsoft 365 and sync settings in cloud.json, and switches the
// console to the live domain.
func (s *Service) Configure(w LiveWiring, req SetupRequest) (Status, error) {
	st := auth.DefaultSettings()
	st.Domain, st.Server, st.BaseDN, st.LDAPS = strings.TrimSpace(req.Domain), strings.TrimSpace(req.Server), strings.TrimSpace(req.BaseDN), req.LDAPS
	st.KerberosOnly = !req.AllowNTLM
	st.SecondFactor, st.AllowPasswordOnly, st.Notice = req.SecondFactor, req.AllowPasswordOnly, req.Notice
	if req.OperatorsGroup != "" {
		st.OperatorsGroup = req.OperatorsGroup
	}
	if req.ApproversGroup != "" {
		st.ApproversGroup = req.ApproversGroup
	}
	if req.IdleLockMinutes > 0 {
		st.IdleLockMinutes = req.IdleLockMinutes
	}
	if req.UsernameFormat != "" {
		st.RADIUS.UsernameFormat = req.UsernameFormat
	}
	dir := filepath.Dir(w.SettingsPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return s.Status(), err
	}
	if st.SecondFactor != "" {
		st.RADIUS.Server = strings.TrimSpace(req.RADIUSServer)
		st.RADIUS.SecretFile = filepath.Join(dir, "radius-secret.bin")
		if req.RADIUSSecret != "" {
			if w.Protect == nil {
				return s.Status(), errors.New("the RADIUS secret can only be protected on Windows")
			}
			plain := []byte(req.RADIUSSecret)
			blob, err := w.Protect(plain)
			for i := range plain {
				plain[i] = 0
			}
			if err != nil {
				return s.Status(), err
			}
			if err := os.WriteFile(st.RADIUS.SecretFile, blob, 0o600); err != nil {
				return s.Status(), err
			}
		}
	}
	if err := st.Validate(); err != nil {
		return s.Status(), err
	}
	cloud, withCloud := req.cloudSettings()
	if err := cloud.Validate(); err != nil {
		return s.Status(), err
	}
	b, err := NewLiveBackend(st, w.Open)
	if err != nil {
		return s.Status(), err
	}
	if err := b.UseCloud(cloud, w.Cloud); err != nil {
		return s.Status(), err
	}
	// cloud.json first: settings.json is what switches a later start to live.
	if withCloud {
		if err := SaveCloudSettings(CloudSettingsPath(w.SettingsPath), cloud); err != nil {
			return s.Status(), err
		}
	}
	raw, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(w.SettingsPath, append(raw, '\n'), 0o640); err != nil {
		return s.Status(), err
	}
	s.mu.Lock()
	s.b = b
	s.mu.Unlock()
	return s.Status(), nil
}
