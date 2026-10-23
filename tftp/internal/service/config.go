// Package service runs administrator-configured sessions and reloads with rollback.
package service

import (
	"encoding/json"
	"errors"
	"github.com/zerotrustgarage/tools/tftp/internal/importer"
	"github.com/zerotrustgarage/tools/tftp/internal/server"
	"github.com/zerotrustgarage/tools/tftp/internal/store"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Root        string            `json:"root"`
	Upload      string            `json:"upload"`
	AuditDir    string            `json:"audit_dir"`
	Bind        string            `json:"bind"`
	Port        int               `json:"port"`
	Allow       []string          `json:"allow"`
	AllowPublic bool              `json:"allow_public"`
	Duration    string            `json:"duration"`
	Indefinite  bool              `json:"indefinite"`
	SinglePort  bool              `json:"single_port"`
	IgnoreCase  bool              `json:"ignore_case"`
	UploadMax   int64             `json:"upload_max"`
	FreeFloor   uint64            `json:"free_floor"`
	Manifest    string            `json:"manifest"`
	Scanner     string            `json:"scanner"`
	Remap       []server.Remap    `json:"remap"`
	Templates   []server.Template `json:"templates"`
	SyslogTLS   string            `json:"syslog_tls"`
	SyslogCA    string            `json:"syslog_ca"`
	EventLog    bool              `json:"event_log"`
}

func Load(name string) (Config, error) {
	var c Config
	f, e := os.Open(name)
	if e != nil {
		return c, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return c, e
	}
	if len(b) > 1<<20 {
		return c, errors.New("service config exceeds 1 MiB")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("trailing config data")
	}
	base := filepath.Dir(name)
	for _, p := range []*string{&c.Root, &c.Upload, &c.AuditDir, &c.Manifest, &c.Scanner, &c.SyslogCA} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	if c.Root == "" {
		return c, errors.New("root is required")
	}
	if c.Port == 0 {
		c.Port = 69
	}
	if c.Duration == "" {
		c.Duration = "1h"
	}
	_, e = c.Server()
	return c, e
}
func (c Config) Server() (server.Config, error) {
	if c.UploadMax < 0 {
		return server.Config{}, errors.New("upload_max must not be negative")
	}
	duration, e := time.ParseDuration(c.Duration)
	if e != nil || duration <= 0 {
		return server.Config{}, errors.New("duration must be positive (indefinite is a separate explicit setting)")
	}
	if c.Indefinite {
		duration = 0
	}
	v := server.Config{Root: c.Root, Upload: c.Upload, Bind: c.Bind, Port: c.Port, Duration: duration, AllowPublic: c.AllowPublic, SinglePort: c.SinglePort, FoldCase: c.IgnoreCase, UploadMax: c.UploadMax, FreeFloor: c.FreeFloor, Manifest: c.Manifest, Remap: c.Remap, Templates: c.Templates}
	for _, a := range c.Allow {
		p, e := netip.ParsePrefix(a)
		if e != nil {
			return v, e
		}
		v.Allow = append(v.Allow, p.Masked())
	}
	if c.Scanner != "" {
		if len(v.Allow) > 0 {
			return v, errors.New("choose allow or scanner")
		}
		v.Allow, e = importer.Scanner(c.Scanner)
		if e != nil {
			return v, e
		}
	}
	if c.Manifest != "" {
		if _, e = store.LoadManifest(c.Manifest); e != nil {
			return v, e
		}
	}
	if c.Port < 1 || c.Port > 65535 {
		return v, errors.New("invalid port")
	}
	if e = server.ValidateRules(v.Remap, v.Templates); e != nil {
		return v, e
	}
	root, e := os.OpenRoot(c.Root)
	if e != nil {
		return v, e
	}
	root.Close()
	return v, nil
}
