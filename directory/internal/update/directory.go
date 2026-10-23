package update

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
)

// DirectoryCommand exposes the explicit update action before Directory has a
// package. The shared checker's directory-v selection contract arrives in D7.
// No network request, installer download or background check is made here.
func DirectoryCommand(ctx context.Context, current string, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("check-updates", flag.ContinueOnError)
	fs.SetOutput(errOut)
	jsonOut := fs.Bool("json", false, "write update status as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("check-updates accepts only --json")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r := Result{Tool: "directory", Current: current, Status: "unreleased", ReleaseURL: "https://github.com/zerotrustgarage/tools/releases?q=directory-v&expanded=true"}
	if *jsonOut {
		return json.NewEncoder(out).Encode(r)
	}
	_, err := fmt.Fprintf(out, "ZTG Directory: unreleased (installed %s).\nRelease checking will be enabled with the first signed package in D7.\n%s\n", current, r.ReleaseURL)
	return err
}
