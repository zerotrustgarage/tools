package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/client"
	"github.com/zerotrustgarage/tools/tftp/internal/session"
	"github.com/zerotrustgarage/tools/tftp/internal/store"
)

var version = "dev"

type Platform interface {
	Directory() (string, error)
	OpenFile() (string, error)
	SaveFile(string) (string, error)
}
type App struct {
	ctx      context.Context
	cancel   context.CancelFunc
	session  *session.Session
	platform Platform
}

func NewApp() *App                                 { return &App{ctx: context.Background(), session: session.New()} }
func (a *App) Version() string                     { return version }
func (a *App) Start(r session.Request) error       { return a.session.Start(a.ctx, r) }
func (a *App) Stop()                               { a.session.Stop() }
func (a *App) Extend(minutes int) error            { return a.session.Extend(minutes) }
func (a *App) Snapshot() session.Snapshot          { return a.session.Snapshot() }
func (a *App) Collected() ([]store.Receipt, error) { return a.session.Collected() }
func (a *App) Promote(storedAs, name string) error { return a.session.Promote(storedAs, name) }

type Interface struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

func (a *App) Interfaces() []Interface {
	out := []Interface{}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, v := range addrs {
			ip, _, e := net.ParseCIDR(v.String())
			if e != nil {
				continue
			}
			address := ip.String()
			if ip.IsLinkLocalUnicast() && ip.To4() == nil {
				address += "%" + i.Name
			}
			out = append(out, Interface{i.Name, address})
		}
	}
	return out
}
func (a *App) Browse(kind string) (string, error) {
	if a.platform == nil {
		return "", errors.New("desktop dialogs unavailable")
	}
	if kind == "directory" {
		return a.platform.Directory()
	}
	return a.platform.OpenFile()
}
func (a *App) ExportAudit() (string, error) {
	if a.platform == nil {
		return "", errors.New("desktop dialogs unavailable")
	}
	name, e := a.platform.SaveFile("ztg-tftp-audit.jsonl")
	if e != nil || name == "" {
		return name, e
	}
	f, e := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	e = a.session.Export(f)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		os.Remove(name)
	}
	return name, e
}

type TransferRequest struct {
	Op     string `json:"op"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Remote string `json:"remote"`
	Local  string `json:"local"`
}

// Client transfers have their own finite lifetime and are canceled when the window closes.
func (a *App) Transfer(r TransferRequest) error {
	if r.Port < 1 || r.Port > 65535 {
		return errors.New("invalid port")
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Hour)
	defer cancel()
	c := client.Config{Address: net.JoinHostPort(r.Host, fmt.Sprint(r.Port)), Name: r.Remote, Mode: "octet", BlockSize: 1468, WindowSize: 8, Timeout: 3}
	if r.Op == "put" {
		f, e := os.Open(r.Local)
		if e != nil {
			return e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("select a regular file")
		}
		_, e = client.Put(ctx, c, f, info.Size())
		return e
	}
	if r.Op != "get" {
		return errors.New("choose get or put")
	}
	f, e := os.CreateTemp(filepath.Dir(r.Local), ".ztg-tftp-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = client.Get(ctx, c, f); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Link(f.Name(), r.Local)
}
