package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zerotrustgarage/tools/directory/console"
	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/auth"
	"github.com/zerotrustgarage/tools/directory/engine/demo"
)

// stdin is read for confirmations (and, in demo mode only, the password).
var stdin io.Reader = os.Stdin

// readSecret prompts without echo on a Windows console; tests replace it.
var readSecret = func(prompt string, errOut io.Writer) ([]byte, error) {
	return auth.ReadPassword(prompt, os.Stdin, errOut)
}

var manageCommands = map[string]bool{"set": true, "clear": true, "enable": true, "disable": true, "unlock": true,
	"reset-password": true, "expires": true, "move": true, "rename": true, "add-member": true, "remove-member": true,
	"delete": true, "new": true, "templates": true, "records": true, "verify-records": true}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// runManage signs in (design 6.5) and runs one change through the same plan,
// rights checks and records as the desktop console. Nothing runs without a
// typed confirmation; there is no flag to skip it (design Q19).
func runManage(command string, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(escapedControlsWriter{out: errOut})
	demoMode := fs.Bool("demo", false, "use the in-memory demo directory (lab.example)")
	settingsPath := fs.String("settings", "", "sign-in settings file (default: the desktop app's settings.json)")
	dataDir := fs.String("data", defaultDataDir(), "folder for change records and templates")
	user := fs.String("user", "", `account to sign in with (DOMAIN\user or UPN)`)
	ticket := fs.String("ticket", "", "ticket reference for the change record")
	jsonOut := fs.Bool("json", false, "write JSON (records and templates)")
	to := fs.String("to", "", "target OU (move)")
	name := fs.String("name", "", "new name (rename)")
	date := fs.String("date", "", "end date YYYY-MM-DD (expires; empty removes it)")
	keepPassword := fs.Bool("no-must-change", false, "reset-password: do not require a change at next logon")
	tmpl := fs.String("template", "", "template name (new)")
	text := fs.String("text", "", "filter text (records)")
	days := fs.Int("days", 0, "only the last N days (records)")
	var fields multiFlag
	fs.Var(&fields, "field", "template field attribute=value (new; repeatable)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *dataDir == "" {
		return errors.New("--data is required on this platform")
	}
	if *demoMode {
		*dataDir = filepath.Join(*dataDir, "demo")
	}
	var b console.Backend
	if *demoMode {
		b = console.NewDemoBackend()
	} else {
		path := *settingsPath
		if path == "" {
			path = filepath.Join(*dataDir, "settings.json")
		}
		s, err := auth.LoadSettings(path)
		if err != nil {
			return fmt.Errorf("%w (run the desktop app once to complete the setup, or use --demo)", err)
		}
		lb, err := console.NewLiveBackend(s, ad.OpenWriter)
		if err != nil {
			return err
		}
		b = lb
	}
	svc, err := console.New(b, console.Config{DataDir: *dataDir, Version: "dev"})
	if err != nil {
		return err
	}
	if *demoMode {
		if err := svc.SeedTemplates(demo.Templates()); err != nil {
			return err
		}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	switch command {
	case "templates":
		list := svc.TemplateList()
		if *jsonOut {
			return enc.Encode(list)
		}
		for _, t := range list.Templates {
			fmt.Fprintf(out, "%s (version %d, %s) -> %s\n", t.Name, t.Version, t.Class, t.OU)
			for _, f := range t.Fields {
				req := ""
				if f.Required {
					req = " (required)"
				}
				fmt.Fprintf(out, "    --field %s=...  %s%s\n", f.Attribute, f.Label, req)
			}
		}
		for _, p := range list.Problems {
			fmt.Fprintln(errOut, "template problem:", p)
		}
		return nil
	case "records":
		es, err := svc.Records(console.RecordQuery{Text: *text, Days: *days, Limit: 200})
		if err != nil {
			return err
		}
		if *jsonOut {
			return enc.Encode(es)
		}
		for _, e := range es {
			fmt.Fprintf(out, "%s  %-14s %-8s %s %s %s\n", e.Time.Format("2006-01-02 15:04:05"), e.Kind, e.Result, e.Technician, e.DN, e.Note)
		}
		return nil
	case "verify-records":
		probs, err := svc.VerifyRecords()
		if err != nil {
			return err
		}
		if len(probs) == 0 {
			fmt.Fprintln(out, "Change record chain intact:", svc.RecordsFolder())
			return nil
		}
		for _, p := range probs {
			fmt.Fprintf(out, "%s %s line %d: %s\n", p.Writer, p.File, p.Line, p.Reason)
		}
		return fmt.Errorf("%d problem(s) in the change record", len(probs))
	}

	// Build the request before signing in, so usage errors come first.
	var req console.EditRequest
	pos := fs.Args()
	need := func(n int) error {
		if len(pos) < n {
			return fmt.Errorf("%s needs %d argument(s); run 'ztg-directory help'", command, n)
		}
		for _, p := range pos {
			if hasControl(p) {
				return errors.New("arguments cannot contain control characters")
			}
		}
		return nil
	}
	switch command {
	case "set":
		if err := need(3); err != nil {
			return err
		}
		req.Edits = []console.Edit{{Kind: "set", DN: pos[0], Attribute: pos[1], Values: pos[2:]}}
	case "clear":
		if err := need(2); err != nil {
			return err
		}
		req.Edits = []console.Edit{{Kind: "set", DN: pos[0], Attribute: pos[1]}}
	case "enable", "disable", "unlock", "delete":
		if err := need(1); err != nil {
			return err
		}
		req.Edits = []console.Edit{{Kind: command, DN: pos[0]}}
	case "reset-password":
		if err := need(1); err != nil {
			return err
		}
		req.Edits = []console.Edit{{Kind: command, DN: pos[0], MustChange: !*keepPassword}}
	case "expires":
		if err := need(1); err != nil {
			return err
		}
		req.Edits = []console.Edit{{Kind: command, DN: pos[0], Date: *date}}
	case "move":
		if err := need(1); err != nil {
			return err
		}
		if *to == "" {
			return errors.New("move needs --to OU")
		}
		req.Edits = []console.Edit{{Kind: command, DN: pos[0], Target: *to}}
	case "rename":
		if err := need(1); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("rename needs --name")
		}
		req.Edits = []console.Edit{{Kind: command, DN: pos[0], Target: *name}}
	case "add-member", "remove-member":
		if err := need(2); err != nil {
			return err
		}
		req.Edits = []console.Edit{{Kind: command, DN: pos[0], Target: pos[1]}}
	case "new":
		if *tmpl == "" {
			return errors.New("new needs --template NAME (see 'ztg-directory templates')")
		}
	}
	req.Ticket = *ticket

	in := bufio.NewReader(stdin)
	if err := signIn(svc, *user, *demoMode, in, out, errOut); err != nil {
		return err
	}
	defer svc.SignOut()

	var pv console.PlanView
	if command == "new" {
		form := map[string]string{}
		for _, f := range fields {
			k, v, ok := strings.Cut(f, "=")
			if !ok || strings.TrimSpace(k) == "" {
				return fmt.Errorf("--field %q must be attribute=value", f)
			}
			form[strings.TrimSpace(k)] = v
		}
		prev, err := svc.PreviewTemplate(*tmpl, form)
		if err != nil {
			return err
		}
		if len(prev.Problems) > 0 {
			return errors.New(strings.Join(prev.Problems, "; "))
		}
		for _, n := range prev.Notes {
			fmt.Fprintln(out, "Note:", n)
		}
		pv, err = svc.PlanTemplate(*tmpl, form, *ticket)
		if err != nil {
			return err
		}
	} else {
		pv, err = svc.PlanEdits(req)
		if err != nil {
			return err
		}
	}
	printPlan(out, pv)
	if pv.Blocked {
		return errors.New("the plan cannot run (see the blocking warnings above)")
	}
	if len(pv.Changes) == 0 {
		return nil
	}
	ar := console.ApplyRequest{PlanID: pv.ID, Hash: pv.Hash}
	if pv.Tier0 {
		fmt.Fprint(out, "This plan touches Tier 0 objects. Type tier0 to confirm: ")
		if strings.TrimSpace(readLine(in)) != "tier0" {
			return errors.New("not confirmed; nothing was changed")
		}
		ar.ConfirmTier0 = true
	}
	if pv.TypedCount {
		fmt.Fprintf(out, "This plan changes %d objects. Type the number of objects to confirm: ", pv.Objects)
		ar.TypedCount, _ = strconv.Atoi(strings.TrimSpace(readLine(in)))
	} else {
		fmt.Fprint(out, "Type yes to apply: ")
		if strings.TrimSpace(readLine(in)) != "yes" {
			return errors.New("not confirmed; nothing was changed")
		}
	}
	res, err := svc.Apply(ar)
	for _, s := range res.Steps {
		fmt.Fprintf(out, "[%s] %s", s.Status, s.Summary)
		if s.Error != "" {
			fmt.Fprintf(out, ": %s", s.Error)
		}
		fmt.Fprintln(out)
		if s.Password != "" {
			fmt.Fprintf(out, "    Temporary password (shown once, not recorded): %s\n", s.Password)
		}
	}
	if err != nil {
		return err
	}
	if !res.OK {
		return errors.New("the plan stopped at a failed step; later steps were not run")
	}
	fmt.Fprintln(out, "Recorded as job", res.Job, "in", svc.RecordsFolder())
	return nil
}

func signIn(svc *console.Service, user string, demoMode bool, in *bufio.Reader, out, errOut io.Writer) error {
	st := svc.Status()
	if st.Notice != "" {
		fmt.Fprintln(errOut, st.Notice)
	}
	if user == "" {
		if !demoMode {
			return errors.New(`--user is required (DOMAIN\user or UPN)`)
		}
		user = "ztg.tech"
	}
	var pw, factor []byte
	var err error
	if demoMode {
		fmt.Fprintf(errOut, "Demo password (%s): ", console.DemoPassword)
		pw = []byte(strings.TrimSpace(readLine(in)))
	} else {
		pw, err = readSecret("Password for "+user+": ", errOut)
		if err != nil {
			return err
		}
	}
	defer func() {
		for i := range pw {
			pw[i] = 0
		}
	}()
	if st.SecondFactor != "" {
		factor, err = readSecret("Second factor (push, phone or passcode): ", errOut)
		if err != nil {
			return err
		}
	}
	st, err = svc.SignIn(console.SignInRequest{User: user, Password: string(pw), Factor: string(factor)})
	for i := range factor {
		factor[i] = 0
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(errOut, "Signed in as %s on %s (%s)\n", st.Account, st.DC, st.Method)
	if st.PasswordOnly {
		fmt.Fprintln(errOut, "Warning: signed in without multifactor authentication; this sign-in is recorded as an open IA.L2-3.5.3 item.")
	}
	return nil
}

func printPlan(out io.Writer, pv console.PlanView) {
	fmt.Fprintf(out, "Plan: %s\nRuns as %s on %s; %d object(s)\n", pv.Title, pv.Technician, pv.DC, pv.Objects)
	for i, c := range pv.Changes {
		fmt.Fprintf(out, "  %d. %s\n", i+1, c.Summary)
		if c.Attribute != "" && (len(c.Before) > 0 || len(c.After) > 0) && c.Kind == "set" {
			fmt.Fprintf(out, "       %s: %q -> %q\n", c.Attribute, c.Before, c.After)
		}
	}
	for _, w := range pv.Warnings {
		tag := "note"
		if w.Blocking {
			tag = "BLOCKED"
		}
		fmt.Fprintf(out, "  [%s] %s\n", tag, w.Message)
	}
}

func readLine(r *bufio.Reader) string {
	s, _ := r.ReadString('\n')
	return s
}

func defaultDataDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "ZTG Directory")
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "ZTG Directory")
	}
	return ""
}
