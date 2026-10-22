//go:build windows

package main

import (
	"context"
	"errors"
	"github.com/zerotrustgarage/tools/tftp/internal/service"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
	"io"
	"os"
)

const serviceName = "ZTGTFTP"

type handler struct {
	name string
	out  io.Writer
}

func (h handler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reload := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx, h.name, reload, h.out) }()
	state := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptParamChange}
	status <- state
	for {
		select {
		case e := <-done:
			if e != nil {
				return true, 1
			}
			return false, 0
		case r := <-requests:
			switch r.Cmd {
			case svc.Interrogate:
				status <- state
			case svc.ParamChange:
				select {
				case reload <- struct{}{}:
				default:
				}
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				if e := <-done; e != nil {
					return true, 1
				}
				return false, 0
			}
		}
	}
}
func runService(ctx context.Context, name string, out io.Writer) error {
	native, e := svc.IsWindowsService()
	if e != nil {
		return e
	}
	if native {
		return svc.Run(serviceName, handler{name, out})
	}
	return service.Run(ctx, name, nil, out)
}
func installService(config string) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	m, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer m.Disconnect()
	s, e := m.CreateService(serviceName, exe, mgr.Config{DisplayName: "ZTG TFTP", Description: "Explicitly configured TFTP sessions. No authentication or encryption; never use for CUI.", StartType: mgr.StartManual, ServiceStartName: `NT AUTHORITY\LocalService`}, "service", "run", "--config", config)
	if e != nil {
		return e
	}
	defer s.Close()
	if e = eventlog.InstallAsEventCreate(serviceName, eventlog.Info|eventlog.Warning|eventlog.Error); e != nil {
		s.Delete()
		return e
	}
	return nil
}
func removeService() error {
	m, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer m.Disconnect()
	s, e := m.OpenService(serviceName)
	if e != nil {
		return e
	}
	defer s.Close()
	state, e := s.Query()
	if e != nil {
		return e
	}
	if state.State != svc.Stopped {
		return errors.New("stop ZTGTFTP before removal")
	}
	if e = s.Delete(); e != nil {
		return e
	}
	return eventlog.Remove(serviceName)
}
