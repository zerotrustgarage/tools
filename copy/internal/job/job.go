// Package job holds versioned jobs independently of a process or user interface.
package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zerotrustgarage/tools/copy/internal/catalog"
	"github.com/zerotrustgarage/tools/copy/internal/cmdline"
)

const Schema = 1
const MaxJSON = 1 << 20

type Job struct {
	Schema      int      `json:"schema"`
	Name        string   `json:"name,omitempty"`
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
	Files       []string `json:"files,omitempty"`
	Options     []string `json:"options,omitempty"`
}

type Issue struct {
	Severity string `json:"severity"`
	Argument string `json:"argument,omitempty"`
	Message  string `json:"message"`
}

func Decode(r io.Reader) (Job, error) {
	var j Job
	if err := DecodeStrict(r, &j); err != nil {
		return j, err
	}
	if j.Schema != Schema {
		return j, fmt.Errorf("unsupported job schema %d", j.Schema)
	}
	return j, nil
}

func DecodeStrict(r io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(r, MaxJSON+1))
	if err != nil {
		return err
	}
	if len(data) > MaxJSON {
		return errors.New("JSON document exceeds 1 MiB")
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("invalid document: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("expected exactly one JSON document")
	}
	return nil
}

func Import(command string) (Job, error) {
	args, err := cmdline.Tokenize(command)
	if err != nil {
		return Job{}, err
	}
	if len(args) < 3 {
		return Job{}, errors.New("expected robocopy SOURCE DESTINATION [files] [options]")
	}
	exe := strings.ToLower(path.Base(strings.ReplaceAll(args[0], "\\", "/")))
	if exe != "robocopy" && exe != "robocopy.exe" {
		return Job{}, errors.New("command must start with robocopy or robocopy.exe; shell scripts are not supported")
	}
	j := Job{Schema: Schema, Source: args[1], Destination: args[2]}
	options := false
	for _, a := range args[3:] {
		if strings.HasPrefix(a, "/") {
			options = true
		}
		if options {
			j.Options = append(j.Options, a)
		} else {
			j.Files = append(j.Files, a)
		}
	}
	return j, nil
}

func textOK(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return r < 32 || r == 127 })
}

func AbsoluteWindowsPath(s string) bool {
	if !textOK(s) || strings.ContainsAny(s, "\"*?<>|") || strings.HasPrefix(s, `\\?\`) || strings.HasPrefix(s, `\\.\`) {
		return false
	}
	if len(s) >= 3 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) && s[1] == ':' && s[2] == '\\' {
		return !strings.Contains(s[2:], ":")
	}
	if strings.HasPrefix(s, `\\`) {
		parts := strings.Split(strings.TrimSuffix(s[2:], "\\"), "\\")
		return len(parts) >= 2 && parts[0] != "" && parts[1] != "" && !strings.Contains(s, ":")
	}
	return false
}

func Key(a string) string {
	return strings.ToUpper(strings.SplitN(a, ":", 2)[0])
}

func (j Job) Issues() []Issue {
	var issues []Issue
	add := func(arg, message string) { issues = append(issues, Issue{"error", arg, message}) }
	if j.Schema != Schema {
		add("", "unsupported schema")
	}
	for _, e := range []struct{ name, value string }{{"source", j.Source}, {"destination", j.Destination}} {
		if !AbsoluteWindowsPath(e.value) {
			add(e.name, "use an absolute drive or UNC path without device prefixes or wildcards")
		}
	}
	if !textOK(j.Name) {
		add("name", "name contains control characters or invalid text")
	}
	for _, file := range j.Files {
		if file == "" || !textOK(file) || strings.ContainsAny(file, "\\/:<>\"|&") || file == "." || file == ".." {
			add(file, "file filters must be names or wildcards, not paths, options or shell operators")
		}
	}
	seen := map[string]string{}
	for i := 0; i < len(j.Options); i++ {
		a := j.Options[i]
		if !textOK(a) {
			add(a, "argument contains control characters or invalid text")
		}
		o, ok := catalog.Lookup(Key(a))
		if !ok {
			add(a, "not recognized by this catalog; retained for review")
			continue
		}
		if old, exists := seen[o.Token]; exists {
			add(a, "duplicate option: "+old)
		}
		seen[o.Token] = a
		value := strings.SplitN(a, ":", 2)
		hasValue := len(value) == 2 && value[1] != ""
		switch o.Argument {
		case "none", "list":
			if len(value) != 1 {
				add(a, "this option does not take a colon value")
			}
		case "value":
			if !hasValue {
				add(a, "a colon value is required")
			}
		case "optional":
			if len(value) == 2 && !hasValue {
				add(a, "an explicit value cannot be empty")
			}
		}
		if hasValue {
			if strings.ContainsAny(value[1], "\x00\r\n") {
				add(a, "invalid option value")
			}
			if min, max, numeric := numericRange(o.Token); numeric {
				n, err := strconv.ParseUint(value[1], 10, 64)
				if err != nil || n < min || n > max {
					add(a, fmt.Sprintf("expected an integer from %d to %d", min, max))
				}
			}
		}
		if o.Argument == "list" {
			start := i + 1
			for i+1 < len(j.Options) && !strings.HasPrefix(j.Options[i+1], "/") {
				i++
				if j.Options[i] == "" || !textOK(j.Options[i]) {
					add(a, "list contains an empty or invalid value")
				}
			}
			if i+1 == start {
				add(a, "requires at least one list value")
			}
		}
		if o.Token == "/M" {
			issues = append(issues, Issue{"warning", a, "changes the source by clearing Archive attributes"})
		}
	}
	for token := range seen {
		o, _ := catalog.Lookup(token)
		for _, c := range o.ConflictsWith {
			if _, ok := seen[c]; ok && token < c {
				add(token, "conflicts with "+c)
			}
		}
	}
	if _, ok := seen["/SECFIX"]; ok {
		security := seen["/SEC"] != "" || seen["/COPYALL"] != "" || strings.ContainsAny(strings.TrimPrefix(strings.ToUpper(seen["/COPY"]), "/COPY:"), "SOU")
		if !security {
			add("/SECFIX", "requires /SEC, /COPYALL or /COPY with S, O or U")
		}
	}
	return issues
}

func numericRange(token string) (uint64, uint64, bool) {
	switch token {
	case "/MT":
		return 1, 128, true
	case "/R":
		return 0, 1000000, true
	case "/W":
		return 0, 86400, true
	case "/LEV", "/MON", "/MOT":
		return 1, 1<<31 - 1, true
	case "/MAX", "/MIN", "/IPG":
		return 0, 1<<63 - 1, true
	}
	return 0, 0, false
}

// Arguments applies only absent defaults, preserving the imported arguments.
// The runner adds its own preview/log controls after enforcing its policy.
func (j Job) Arguments() []string {
	args := []string{j.Source, j.Destination}
	args = append(args, j.Files...)
	args = append(args, j.Options...)
	seen := map[string]bool{}
	for _, a := range j.Options {
		seen[Key(a)] = true
	}
	for _, a := range []string{"/E", "/DCOPY:DAT", "/R:2", "/W:5", "/XJ"} {
		k := Key(a)
		if k == "/E" && (seen["/S"] || seen["/LEV"] || seen["/MIR"]) || k == "/DCOPY" && seen["/NODCOPY"] {
			continue
		}
		if !seen[k] {
			args = append(args, a)
		}
	}
	return args
}

func (j Job) Command() (string, error) {
	return cmdline.Render(append([]string{"robocopy.exe"}, j.Arguments()...))
}
