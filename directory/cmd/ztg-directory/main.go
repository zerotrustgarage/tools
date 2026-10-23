// ztg-directory is a read-only command line client for Active Directory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/internal/update"
)

const (
	defaultTimeout = 15 * time.Second
	maxTimeout     = 10 * time.Minute
	defaultLimit   = 100
	maxLimit       = 1000
)

type clientFactory func(context.Context, ad.Config) (ad.Client, error)
type updateCommand func(context.Context, string, []string, io.Writer, io.Writer) error

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, ad.Open, update.DirectoryCommand); err != nil {
		fmt.Fprintf(os.Stderr, "ztg-directory: %q\n", err.Error())
		os.Exit(2)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer, open clientFactory, checkUpdates updateCommand) error {
	if len(args) == 0 {
		usage(out)
		return nil
	}
	command, args := args[0], args[1:]
	switch command {
	case "help", "--help", "-h":
		usage(out)
		return nil
	case "version":
		if len(args) != 0 {
			return errors.New("version accepts no arguments")
		}
		_, err := fmt.Fprintln(out, "ztg-directory dev")
		return err
	case "check-updates":
		return checkUpdates(ctx, "dev", args, out, errOut)
	case "search", "object", "groups", "last-logon":
		return runDirectoryCommand(ctx, command, args, out, errOut, open)
	default:
		return fmt.Errorf("unknown command %q; run 'ztg-directory help' for usage", command)
	}
}

func runDirectoryCommand(ctx context.Context, command string, args []string, out, errOut io.Writer, open clientFactory) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(escapedControlsWriter{out: errOut})
	server := fs.String("server", "", "domain controller host or host:port")
	baseDN := fs.String("base-dn", "", "LDAP search base distinguished name")
	ldaps := fs.Bool("ldaps", false, "connect with LDAPS; otherwise use signed and sealed Negotiate LDAP")
	timeout := fs.Duration("timeout", defaultTimeout, "operation timeout (for example 15s or 2m)")
	jsonOutput := fs.Bool("json", false, "write JSON output")
	query := fs.String("query", "", "text to search in name, account name, UPN, and email")
	limit := fs.Int("limit", defaultLimit, "maximum search results (1 to 1000)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if err := validateConnection(*server, *baseDN, *timeout); err != nil {
		return err
	}
	var dn string
	switch command {
	case "search":
		if strings.TrimSpace(*query) == "" {
			return errors.New("search requires a non-empty --query")
		}
		if len(*query) > 256 {
			return errors.New("--query must be 256 characters or fewer")
		}
		if hasControl(*query) {
			return errors.New("--query cannot contain control characters")
		}
		if *limit < 1 || *limit > maxLimit {
			return fmt.Errorf("--limit must be between 1 and %d", maxLimit)
		}
		if fs.NArg() != 0 {
			return errors.New("search accepts no positional arguments")
		}
	case "object", "groups", "last-logon":
		if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
			return fmt.Errorf("%s requires exactly one distinguished name", command)
		}
		dn = fs.Arg(0)
		if hasControl(dn) {
			return errors.New("distinguished name cannot contain control characters")
		}
		if len(dn) > 4096 {
			return errors.New("distinguished name must be 4096 characters or fewer")
		}
	}
	config := ad.Config{Server: strings.TrimSpace(*server), BaseDN: strings.TrimSpace(*baseDN), LDAPS: *ldaps, Timeout: *timeout}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client, err := open(ctx, config)
	if err != nil {
		return err
	}
	defer client.Close()
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(true)
	switch command {
	case "search":
		result, err := client.Search(ctx, ad.SearchRequest{Query: strings.TrimSpace(*query), Limit: *limit})
		if err != nil {
			return err
		}
		if *jsonOutput {
			return enc.Encode(result)
		}
		return writeObjects(out, result.Objects, result.Truncated)
	case "object":
		object, err := client.Get(ctx, dn)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return enc.Encode(object)
		}
		return writeObject(out, object)
	case "groups":
		groups, err := client.EffectiveGroups(ctx, dn)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return enc.Encode(groups)
		}
		return writeGroups(out, groups)
	case "last-logon":
		result, err := client.LastLogon(ctx, dn)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return enc.Encode(result)
		}
		return writeLastLogon(out, result)
	}
	return errors.New("unsupported command")
}

func validateConnection(server, baseDN string, timeout time.Duration) error {
	if strings.TrimSpace(server) == "" {
		return errors.New("--server is required")
	}
	if hasControl(server) {
		return errors.New("--server contains invalid characters")
	}
	if strings.TrimSpace(baseDN) == "" {
		return errors.New("--base-dn is required")
	}
	if hasControl(baseDN) {
		return errors.New("--base-dn contains invalid characters")
	}
	if timeout <= 0 || timeout > maxTimeout {
		return fmt.Errorf("--timeout must be greater than 0 and no more than %s", maxTimeout)
	}
	return nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

type escapedControlsWriter struct{ out io.Writer }

func (w escapedControlsWriter) Write(p []byte) (int, error) {
	text := strings.Builder{}
	for _, r := range string(p) {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			text.WriteString(strconv.QuoteRune(r))
		} else {
			text.WriteRune(r)
		}
	}
	_, err := io.WriteString(w.out, text.String())
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func writeObjects(out io.Writer, objects []ad.Object, truncated bool) error {
	for _, object := range objects {
		if err := writeObject(out, object); err != nil {
			return err
		}
	}
	if truncated {
		_, err := fmt.Fprintln(out, "Results truncated. Narrow the query or increase --limit within the allowed range.")
		return err
	}
	if len(objects) == 0 {
		_, err := fmt.Fprintln(out, "No matching objects.")
		return err
	}
	return nil
}

func writeObject(out io.Writer, object ad.Object) error {
	if _, err := fmt.Fprintf(out, "DN: %q\n", object.DN); err != nil {
		return err
	}
	keys := make([]string, 0, len(object.Attributes))
	for key := range object.Attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := fmt.Fprintf(out, "%q: %q\n", key, object.Attributes[key]); err != nil {
			return err
		}
	}
	return nil
}

func writeGroups(out io.Writer, groups []ad.Group) error {
	if len(groups) == 0 {
		_, err := fmt.Fprintln(out, "No effective groups found.")
		return err
	}
	for _, group := range groups {
		if _, err := fmt.Fprintf(out, "%q  DN=%q  SID=%q  primary=%t  resolved=%t\n", group.Name, group.DN, group.SID, group.Primary, group.Resolved); err != nil {
			return err
		}
	}
	return nil
}

func writeLastLogon(out io.Writer, result ad.LastLogonResult) error {
	format := func(value time.Time) string {
		if value.IsZero() {
			return "never or unavailable"
		}
		return value.UTC().Format(time.RFC3339)
	}
	if _, err := fmt.Fprintf(out, "Last logon on selected DC: %s\nReplicated last logon timestamp: %s\n", format(result.LastLogon), format(result.LastLogonTimestamp)); err != nil {
		return err
	}
	if result.Partial {
		if result.Warning == "" {
			result.Warning = "AD lastLogon is local to each domain controller; this result is partial."
		}
		_, err := fmt.Fprintf(out, "Warning: %q\n", result.Warning)
		return err
	}
	return nil
}

func usage(out io.Writer) {
	fmt.Fprintln(out, `ztg-directory: read-only Active Directory lookups

Usage:
  ztg-directory search --server HOST --base-dn DN --query TEXT [--limit N] [--ldaps] [--timeout DURATION] [--json]
  ztg-directory object --server HOST --base-dn DN [--ldaps] [--timeout DURATION] [--json] DISTINGUISHED_NAME
  ztg-directory groups --server HOST --base-dn DN [--ldaps] [--timeout DURATION] [--json] DISTINGUISHED_NAME
  ztg-directory last-logon --server HOST --base-dn DN [--ldaps] [--timeout DURATION] [--json] DISTINGUISHED_NAME
  ztg-directory check-updates [--json]
  ztg-directory version
  ztg-directory help

LDAP uses the current Windows identity. This command does not accept credentials or perform writes.`)
	fmt.Fprintln(out, "Group results reflect the memberships returned by this lookup and do not describe all access granted by ACLs.")
	fmt.Fprintln(out, "Last logon is queried from the selected DC; AD's non-replicated lastLogon value can be incomplete across DCs.")
}
