//go:build !windows

package service

import "errors"

func openEventLog() (eventSink, error) { return nil, errors.New("event_log requires Windows") }
