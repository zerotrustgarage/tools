package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/zerotrustgarage/tools/tftp/internal/service"
	"io"
	"path/filepath"
)

func serviceCommand(ctx context.Context, args []string, out, errOut io.Writer) error {
	op := "run"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		op = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(errOut)
	name := fs.String("config", "", "administrator-owned JSON configuration path")
	fs.Usage = func() {
		fmt.Fprintln(errOut, "Usage: ztg-tftp service [run|check|install|remove] --config FILE\nInstall/remove require administrator rights on Windows. Services are installed stopped with manual start.\n"+warning)
		fs.PrintDefaults()
	}
	if e := fs.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return nil
		}
		return e
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected service argument")
	}
	if op == "remove" {
		return removeService()
	}
	if *name == "" {
		return errors.New("service requires --config")
	}
	abs, e := filepath.Abs(*name)
	if e != nil {
		return e
	}
	if _, e = service.Load(abs); e != nil {
		return e
	}
	switch op {
	case "check":
		fmt.Fprintln(out, "Configuration valid")
		return nil
	case "install":
		return installService(abs)
	case "run":
		return runService(ctx, abs, out)
	default:
		return errors.New("unknown service operation")
	}
}
