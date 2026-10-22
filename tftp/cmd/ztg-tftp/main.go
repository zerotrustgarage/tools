package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/client"
	"github.com/zerotrustgarage/tools/tftp/internal/server"
	"github.com/zerotrustgarage/tools/tftp/internal/store"
	"github.com/zerotrustgarage/tools/tftp/internal/update"
)

var version = "dev"

const warning = "TFTP has no authentication or encryption. Never use for CUI. Prefer SCP, SFTP or HTTPS."

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if e := run(ctx, os.Args[1:], os.Stdout, os.Stderr); e != nil {
		fmt.Fprintln(os.Stderr, "ztg-tftp:", e)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(out, "ZTG TFTP\n\nCommands: serve, get, put, findings, version, check-updates [--json]\nUse <command> --help for flags.\n\n"+warning)
		return nil
	}
	if args[0] == "version" {
		fmt.Fprintln(out, "ztg-tftp", version)
		return nil
	}
	if args[0] == "check-updates" {
		return update.Command(ctx, "tftp", version, args[1:], out, errOut)
	}
	fmt.Fprintln(errOut, warning)
	switch args[0] {
	case "serve":
		return serve(ctx, args[1:], out, errOut)
	case "get", "put":
		return transfer(ctx, args[0], args[1:], out, errOut)
	case "findings":
		return rescan(args[1:], out, errOut)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
func serve(ctx context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(errOut)
	c := server.Config{}
	fs.StringVar(&c.Bind, "bind", "0.0.0.0", "local IP; wildcard expands to eligible IPv4 interfaces (use :: for IPv6)")
	fs.IntVar(&c.Port, "port", 69, "UDP port; use a high port for unprivileged testing")
	fs.DurationVar(&c.Duration, "for", time.Hour, "session duration; must be positive")
	fs.BoolVar(&c.AllowPublic, "allow-public", false, "explicitly permit public local addresses")
	fs.BoolVar(&c.SinglePort, "single-port", false, "answer all transfers from the listening port")
	fs.BoolVar(&c.FoldCase, "ignore-case", false, "case-insensitive file lookup; refuse ambiguous names")
	fs.IntVar(&c.MaxBlock, "blksize-max", 1468, "maximum negotiated block size (8..65464)")
	fs.IntVar(&c.MaxWindow, "windowsize-max", 16, "maximum negotiated window size (1..64)")
	fs.StringVar(&c.Upload, "upload", "", "opt-in drop box outside the served root; uploads are never served")
	fs.Int64Var(&c.UploadMax, "upload-max", store.DefaultUploadMax, "maximum bytes per upload (default 256 MiB)")
	fs.Uint64Var(&c.FreeFloor, "free-floor", store.DefaultFreeFloor, "minimum free bytes to preserve in the drop box (must be positive)")
	var allow, dir string
	fs.StringVar(&allow, "allow", "", "comma-separated source CIDRs; default: bound interface subnets")
	fs.StringVar(&dir, "audit-dir", "", "session directory outside served root; default: sibling .ztg-tftp-sessions/<id>")
	fs.Usage = func() {
		fmt.Fprintln(errOut, "Usage: ztg-tftp serve [flags] ROOT\nRead-only unless --upload selects a separate drop box.\n"+warning)
		fs.PrintDefaults()
	}
	if e := fs.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return nil
		}
		return e
	}
	if fs.NArg() != 1 {
		return errors.New("serve requires a root directory (flags go before ROOT)")
	}
	if c.Upload != "" && (c.UploadMax <= 0 || c.FreeFloor == 0) {
		return errors.New("upload-max and free-floor must be positive")
	}
	root, e := filepath.Abs(fs.Arg(0))
	if e != nil {
		return e
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	c.Root = root
	if allow != "" {
		for _, s := range strings.Split(allow, ",") {
			p, e := netip.ParsePrefix(strings.TrimSpace(s))
			if e != nil {
				return fmt.Errorf("allow: %w", e)
			}
			c.Allow = append(c.Allow, p.Masked())
		}
	}
	if dir == "" {
		dir = filepath.Join(filepath.Dir(root), ".ztg-tftp-sessions", server.ID())
	}
	// Resolve the existing parent before creation to keep logs outside the root,
	// even when --audit-dir uses symlinks. No audit files are served remotely.
	dir, e = filepath.Abs(dir)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	dir, e = filepath.EvalSymlinks(dir)
	if e != nil {
		return e
	}
	rel, e := filepath.Rel(root, dir)
	if e != nil {
		return e
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("audit directory must be outside served root")
	}
	log, e := os.OpenFile(filepath.Join(dir, "audit.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	c.Audit = log
	s, e := server.Start(ctx, c)
	if e != nil {
		log.Close()
		return e
	}
	fmt.Fprintln(out, "Read-only root:", root)
	if c.Upload != "" {
		fmt.Fprintln(out, "Upload drop box:", c.Upload)
	}
	for _, a := range s.Addresses() {
		fmt.Fprintln(out, "Listening:", a)
	}
	fmt.Fprintln(out, "Session duration:", c.Duration)
	fmt.Fprintln(out, "Audit:", log.Name())
	e = s.Wait()
	closeErr := log.Close()
	if e != nil {
		return e
	}
	return closeErr
}

func rescan(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("findings", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		fmt.Fprintln(errOut, "Usage: ztg-tftp findings DROP_BOX\nRescan committed configurations; output JSON Lines with rule names and line numbers only.\n"+warning)
	}
	if e := fs.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return nil
		}
		return e
	}
	if fs.NArg() != 1 {
		return errors.New("findings requires a drop box directory")
	}
	enc := json.NewEncoder(out)
	return store.Rescan(fs.Arg(0), func(r store.Receipt) error { return enc.Encode(r) })
}
func transfer(ctx context.Context, op string, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet(op, flag.ContinueOnError)
	fs.SetOutput(errOut)
	c := client.Config{}
	fs.IntVar(&c.BlockSize, "blksize", 1468, "requested block size (8..65464)")
	fs.IntVar(&c.WindowSize, "windowsize", 8, "requested window size (1..64)")
	fs.IntVar(&c.Timeout, "timeout", 3, "per-packet timeout seconds (1..255)")
	fs.StringVar(&c.Mode, "mode", "octet", "octet or netascii")
	var port int
	var duration time.Duration
	fs.IntVar(&port, "port", 69, "server UDP port")
	fs.DurationVar(&duration, "for", time.Hour, "maximum total transfer duration")
	fs.Usage = func() {
		fmt.Fprintf(errOut, "Usage: ztg-tftp %s [flags] HOST FILE [DESTINATION]\nget: FILE is remote, DESTINATION is local (default: base name).\nput: FILE is local, DESTINATION is remote (default: base name).\n%s\n", op, warning)
		fs.PrintDefaults()
	}
	if e := fs.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return nil
		}
		return e
	}
	if fs.NArg() < 2 || fs.NArg() > 3 {
		return errors.New("expected HOST FILE [DESTINATION] (flags go first)")
	}
	if port < 1 || port > 65535 || duration <= 0 {
		return errors.New("invalid port or duration")
	}
	c.Address = net.JoinHostPort(strings.Trim(fs.Arg(0), "[]"), fmt.Sprint(port))
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	if op == "put" {
		f, e := os.Open(fs.Arg(1))
		if e != nil {
			return e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("put requires a regular file")
		}
		c.Name = filepath.Base(fs.Arg(1))
		if fs.NArg() == 3 {
			c.Name = fs.Arg(2)
		}
		stats, e := client.Put(ctx, c, f, info.Size())
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(stats)
	}
	c.Name = fs.Arg(1)
	dest := filepath.Base(strings.ReplaceAll(c.Name, "\\", "/"))
	if fs.NArg() == 3 {
		dest = fs.Arg(2)
	}
	if _, e := os.Lstat(dest); e == nil {
		return fmt.Errorf("destination already exists: %s", dest)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	// Write a private temporary file and atomically link it into place without
	// overwriting an existing target. A failed get leaves no partial destination.
	f, e := os.CreateTemp(filepath.Dir(dest), ".ztg-tftp-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	stats, e := client.Get(ctx, c, f)
	if e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Link(f.Name(), dest); e != nil {
		return fmt.Errorf("commit download (destination must not exist): %w", e)
	}
	return json.NewEncoder(out).Encode(stats)
}
