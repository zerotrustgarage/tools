package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zerotrustgarage/tools/copy/internal/bits"
	"github.com/zerotrustgarage/tools/copy/internal/private"
)

func bitsCommand(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: ztg-copy bits flags|command|validate|export [options] [BITS-JOB.json]")
	}
	fs := flag.NewFlagSet("bits "+args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	asJSON := fs.Bool("json", false, "output structured results")
	query := fs.String("search", "", "flags: filter the BITS parameter reference")
	output := fs.String("output", "", "export: new PowerShell script file")
	if err := fs.Parse(args[1:]); err != nil {
		return helpError(err)
	}
	if args[0] == "flags" {
		if fs.NArg() != 0 || *output != "" {
			return errors.New("bits flags accepts --search and --json")
		}
		entries := []bits.Entry{}
		for _, entry := range bits.Catalog() {
			if strings.Contains(strings.ToLower(entry.Name+" "+entry.Help+" "+entry.Group), strings.ToLower(*query)) {
				entries = append(entries, entry)
			}
		}
		if *asJSON {
			return writeJSON(out, entries)
		}
		for _, entry := range entries {
			marker := ""
			if !entry.Available {
				marker = " [reference only]"
			}
			fmt.Fprintf(out, "-%s%s: %s\n", entry.Name, marker, entry.Help)
		}
		return nil
	}
	if args[0] != "command" && args[0] != "validate" && args[0] != "export" {
		return errors.New("unknown BITS command; use flags, command, validate or export")
	}
	if fs.NArg() != 1 || *query != "" || (args[0] != "export" && *output != "") {
		return errors.New("pass one BITS job JSON filename; flags go before it")
	}
	file, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer file.Close()
	j, err := bits.Decode(file)
	if err != nil {
		return err
	}
	view := bits.Describe(j)
	if *asJSON {
		if err := writeJSON(out, view); err != nil {
			return err
		}
	}
	command, err := bits.Command(j)
	if err != nil {
		return err
	}
	if args[0] == "export" {
		if *output == "" {
			return errors.New("bits export requires --output NEW-SCRIPT.ps1")
		}
		data := []byte("\xef\xbb\xbf" + strings.ReplaceAll(view.Script, "\n", "\r\n"))
		return private.WriteNewBytes(*output, data)
	}
	if !*asJSON {
		if args[0] == "command" {
			fmt.Fprintln(out, command)
		}
		for _, issue := range view.Issues {
			fmt.Fprintf(errOut, "%s %s: %s\n", issue.Severity, issue.Argument, issue.Message)
		}
	}
	return nil
}
