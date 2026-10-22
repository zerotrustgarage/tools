//go:build !windows

package main

import (
	"context"
	"errors"
	"github.com/zerotrustgarage/tools/tftp/internal/service"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func runService(ctx context.Context, name string, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP)
	defer signal.Stop(signals)
	reload := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-signals:
				select {
				case reload <- struct{}{}:
				default:
				}
			}
		}
	}()
	return service.Run(ctx, name, reload, out)
}
func installService(string) error { return errors.New("use the supplied systemd unit on Linux") }
func removeService() error        { return errors.New("use systemctl to remove the Linux service") }
