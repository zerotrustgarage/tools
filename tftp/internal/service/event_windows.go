//go:build windows

package service

import "golang.org/x/sys/windows/svc/eventlog"

type windowsEvents struct{ *eventlog.Log }

func (e windowsEvents) Write(s string) error { return e.Info(1, s) }
func openEventLog() (eventSink, error)       { v, e := eventlog.Open("ZTGTFTP"); return windowsEvents{v}, e }
