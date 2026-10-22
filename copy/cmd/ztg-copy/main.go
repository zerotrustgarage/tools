package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/catalog"
	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/private"
	"github.com/zerotrustgarage/tools/copy/internal/template"
	"github.com/zerotrustgarage/tools/copy/internal/update"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ztg-copy:", err)
		os.Exit(3)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(out, "ZTG Copy\n\nCommands:\n  flags [--search TEXT] [--json]\n  explain [--json] 'robocopy SOURCE DESTINATION [options]'\n  import [--output FILE] 'robocopy SOURCE DESTINATION [options]'\n  validate JOB.json\n  template list|show|save|command|export|import\n  version\n  check-updates [--json]\n\nCLI designer preview. Production plan/run, desktop and release packaging are in development.\nCommands never invoke a shell. Printed commands use direct Windows process quoting.")
		return nil
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return errors.New("version accepts no arguments")
		}
		fmt.Fprintln(out, "ztg-copy", version)
		return nil
	case "check-updates":
		return update.Command(ctx, "copy", version, args[1:], out, errOut)
	case "flags":
		return flagsCommand(args[1:], out, errOut)
	case "explain", "import":
		return commandImport(args[0], args[1:], out, errOut)
	case "validate":
		if len(args) != 2 {
			return errors.New("usage: ztg-copy validate JOB.json")
		}
		j, err := readJob(args[1])
		if err != nil {
			return err
		}
		if err := explain(j, out, false); err != nil {
			return err
		}
		return validate(j)
	case "template":
		return templates(args[1:], out, errOut)
	default:
		return fmt.Errorf("unknown command %q; use --help", args[0])
	}
}

func flagsCommand(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("flags", flag.ContinueOnError)
	fs.SetOutput(errOut)
	query := fs.String("search", "", "search tokens, descriptions, handling and risk tags")
	asJSON := fs.Bool("json", false, "output structured catalog entries")
	if err := fs.Parse(args); err != nil {
		return helpError(err)
	}
	if fs.NArg() != 0 {
		return errors.New("flags accepts only --search and --json")
	}
	entries := catalog.Search(*query)
	if *asJSON {
		return writeJSON(out, entries)
	}
	for _, o := range entries {
		fmt.Fprintf(out, "%s: %s\n", o.Syntax, o.Help)
		if o.Handling != "" {
			fmt.Fprintf(out, "  %s\n", o.Handling)
		}
	}
	return nil
}

func commandImport(command string, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(errOut)
	asJSON := fs.Bool("json", false, "output structured explanation")
	output := fs.String("output", "", "import: write a new job file; refuses overwrite")
	if err := fs.Parse(args); err != nil {
		return helpError(err)
	}
	if fs.NArg() != 1 {
		return errors.New("pass one quoted Robocopy command; options go before it")
	}
	if command == "explain" && *output != "" {
		return errors.New("--output is only supported by import")
	}
	j, err := job.Import(fs.Arg(0))
	if err != nil {
		return err
	}
	if command == "explain" {
		return explain(j, out, *asJSON)
	}
	if *output == "" {
		return writeJSON(out, j)
	}
	if err := private.WriteNew(*output, j); err != nil {
		return err
	}
	fmt.Fprintln(out, "Saved", *output)
	return explain(j, out, false)
}

type explainedOption struct {
	Argument string          `json:"argument"`
	Known    bool            `json:"known"`
	Details  *catalog.Option `json:"details,omitempty"`
}

func explain(j job.Job, out io.Writer, asJSON bool) error {
	command, err := j.Command()
	if err != nil {
		return err
	}
	options := []explainedOption{}
	for _, a := range j.Options {
		if !strings.HasPrefix(a, "/") {
			continue
		}
		o, ok := catalog.Lookup(job.Key(a))
		item := explainedOption{Argument: a, Known: ok}
		if ok {
			item.Details = &o
		}
		options = append(options, item)
	}
	issues := j.Issues()
	if issues == nil {
		issues = []job.Issue{}
	}
	if asJSON {
		return writeJSON(out, struct {
			Job     job.Job           `json:"job"`
			Command string            `json:"direct_process_command"`
			Options []explainedOption `json:"options"`
			Issues  []job.Issue       `json:"issues"`
		}{j, command, options, issues})
	}
	fmt.Fprintf(out, "Source: %s\nDestination: %s\nDirect Windows process command:\n%s\n", j.Source, j.Destination, command)
	fmt.Fprintln(out, "Absent defaults: /E /DCOPY:DAT /R:2 /W:5 /XJ. Preview and convergence are required by the runner.")
	for _, item := range options {
		if item.Known {
			fmt.Fprintf(out, "%s: %s\n", item.Argument, item.Details.Help)
			if len(item.Details.Tags) != 0 {
				fmt.Fprintf(out, "  Tags: %s\n", strings.Join(item.Details.Tags, ", "))
			}
			if item.Details.Handling != "" {
				fmt.Fprintf(out, "  %s\n", item.Details.Handling)
			}
		} else {
			fmt.Fprintf(out, "%s: UNKNOWN, retained for review\n", item.Argument)
		}
	}
	for _, issue := range issues {
		fmt.Fprintf(out, "%s %s: %s\n", strings.ToUpper(issue.Severity), issue.Argument, issue.Message)
	}
	fmt.Fprintln(out, "Static explanation only. Installed-engine support, filesystem guards and transport are not probed.")
	return nil
}

func validate(j job.Job) error {
	for _, issue := range j.Issues() {
		if issue.Severity == "error" {
			return fmt.Errorf("job requires review: %s: %s", issue.Argument, issue.Message)
		}
	}
	return nil
}

func readJob(name string) (job.Job, error) {
	f, err := os.Open(name)
	if err != nil {
		return job.Job{}, err
	}
	defer f.Close()
	return job.Decode(f)
}

func writeJSON(out io.Writer, value any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func helpError(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

type assignments map[string]string

func (v *assignments) String() string { return "" }
func (v *assignments) Set(s string) error {
	k, value, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return errors.New("--set requires name=value")
	}
	if _, exists := (*v)[k]; exists {
		return fmt.Errorf("duplicate --set %s", k)
	}
	(*v)[k] = value
	return nil
}

func templates(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("template requires list, show, save, command, export or import")
	}
	dir, err := template.DefaultDir()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("template "+args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	from := fs.String("from", "", "save: job JSON to save")
	command := fs.String("command", "", "save: Robocopy command to import")
	accept := fs.Bool("accept", false, "explicitly accept the displayed imported template/command")
	tag := fs.String("tag", "", "list: filter by tag")
	values := assignments{}
	fs.Var(&values, "set", "command: placeholder name=value (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
		return helpError(err)
	}
	require := func(n int) error {
		if fs.NArg() != n {
			return errors.New("wrong argument count; template flags go before positional arguments")
		}
		return nil
	}
	switch args[0] {
	case "list":
		if err := require(0); err != nil {
			return err
		}
		ids, err := template.List(dir)
		if err != nil {
			return err
		}
		for _, id := range ids {
			t, err := template.Load(dir, id)
			if err != nil {
				return fmt.Errorf("template %s: %w", id, err)
			}
			if *tag == "" || contains(t.Tags, *tag) {
				fmt.Fprintf(out, "%s\t%s\n", id, t.Description)
			}
		}
		return nil
	case "show", "command", "export":
		n := 1
		if args[0] == "export" {
			n = 2
		}
		if err := require(n); err != nil {
			return err
		}
		t, err := template.Load(dir, fs.Arg(0))
		if err != nil {
			return err
		}
		if args[0] == "show" {
			if err := writeJSON(out, t); err != nil {
				return err
			}
			return explain(t.Job, out, false)
		}
		if args[0] == "export" {
			return private.WriteNew(fs.Arg(1), t)
		}
		host, err := os.Hostname()
		if err != nil {
			return err
		}
		j, err := t.Resolve(values, time.Now(), host, os.Getenv("USERNAME"))
		if err != nil {
			return err
		}
		return explain(j, out, false)
	case "save":
		if err := require(1); err != nil {
			return err
		}
		if (*from == "") == (*command == "") {
			return errors.New("save requires exactly one of --from JOB.json or --command COMMAND")
		}
		var j job.Job
		if *from != "" {
			j, err = readJob(*from)
		} else {
			j, err = job.Import(*command)
		}
		if err != nil {
			return err
		}
		if err := explain(j, out, false); err != nil {
			return err
		}
		if *command != "" && !*accept {
			return errors.New("review the explanation, then repeat with --accept to save the imported command")
		}
		return template.Save(dir, template.Template{Schema: 1, CatalogVersion: 1, ID: fs.Arg(0), Job: j})
	case "import":
		if err := require(1); err != nil {
			return err
		}
		file, err := os.Open(fs.Arg(0))
		if err != nil {
			return err
		}
		defer file.Close()
		var t template.Template
		if err := job.DecodeStrict(file, &t); err != nil {
			return err
		}
		if err := t.Validate(); err != nil {
			return err
		}
		if err := writeJSON(out, t); err != nil {
			return err
		}
		if err := explain(t.Job, out, false); err != nil {
			return err
		}
		if !*accept {
			return errors.New("review the displayed template, then repeat with --accept to save it")
		}
		return template.Save(dir, t)
	default:
		return fmt.Errorf("unknown template command %q", args[0])
	}
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
