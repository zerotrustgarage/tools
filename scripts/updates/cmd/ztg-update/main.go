// Command ztg-update provides checks for all tools, including applications still in development.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	update "github.com/zerotrustgarage/tools/update-check"
)

func main() {
	tool := flag.String("tool", "", "scanner, readout, copy or tftp")
	current := flag.String("current", "dev", "installed application version")
	jsonOut := flag.Bool("json", false, "write update status as JSON")
	flag.Parse()
	args := flag.Args()
	if *jsonOut {
		args = append([]string{"--json"}, args...)
	}
	if err := update.Command(context.Background(), *tool, *current, args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
