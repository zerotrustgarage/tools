package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	SaveDownload() (string, error)
}
type App struct {
	ctx          context.Context
	cancel       context.CancelFunc
	session      *session.Session
	platform     Platform
	transferMu   sync.Mutex
	transfers    sync.WaitGroup
	shuttingDown bool
}

func NewApp() *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{ctx: ctx, cancel: cancel, session: session.New()}
}
func (a *App) Version() string { return version }
func (a *App) Start(r session.Request) error {
	a.transferMu.Lock()
	defer a.transferMu.Unlock()
	if a.shuttingDown {
		return errors.New("application is shutting down")
	}
	return a.session.Start(a.ctx, r)
}
func (a *App) Stop() { a.session.Stop() }
func (a *App) shutdown() {
	a.transferMu.Lock()
	a.shuttingDown = true
	a.transferMu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
	a.Stop()
	a.transfers.Wait()
}
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
	if kind == "save" {
		return a.platform.SaveDownload()
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
	if r.Op != "get" && r.Op != "put" {
		return errors.New("choose get or put")
	}
	if r.Host == "" || strings.TrimSpace(r.Host) != r.Host || strings.ContainsAny(r.Host, " \t\r\n") {
		return errors.New("enter a valid server host")
	}
	if r.Remote == "" {
		return errors.New("enter a remote file name")
	}
	if r.Local == "" {
		return errors.New("choose a local file")
	}
	a.transferMu.Lock()
	if a.shuttingDown {
		a.transferMu.Unlock()
		return errors.New("application is shutting down")
	}
	a.transfers.Add(1)
	a.transferMu.Unlock()
	defer a.transfers.Done()
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
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(r.Local), ".ztg-tftp-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = client.Get(ctx, c, f); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
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
