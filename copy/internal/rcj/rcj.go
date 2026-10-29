// Package rcj reads and writes Robocopy job files (.RCJ, what /SAVE: writes and
// /JOB: reads). Reading never runs anything: the text becomes a job.Job whose
// every switch is checked against the catalog, unknown arguments are kept and
// flagged, and the caller must show the explanation before saving. Writing
// emits only catalog options, never /REG and never a credential. See design
// section 9.4 for the format this package accepts and the decisions behind it.
package rcj

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zerotrustgarage/tools/copy/internal/catalog"
	"github.com/zerotrustgarage/tools/copy/internal/cmdline"
	"github.com/zerotrustgarage/tools/copy/internal/job"
)

const (
	MaxBytes = 1 << 20
	MaxLines = 10000
	// NameMarker carries Job.Name through a file; Robocopy sees a comment.
	NameMarker = "ZTG-Copy-Name:"
	maxName    = 256
)

// Result is an imported job with what the import itself had to say. Notes are
// separate from job.Issues, which the caller still evaluates.
type Result struct {
	Job      job.Job
	Encoding string
	Notes    []job.Issue
}

// ParseBytes decodes and parses a job file.
func ParseBytes(data []byte) (Result, error) {
	text, encoding, err := Decode(data)
	if err != nil {
		return Result{}, err
	}
	r, err := Parse(text)
	if err != nil {
		return Result{}, err
	}
	r.Encoding = encoding
	if encoding == Windows1252 {
		r.Notes = append(r.Notes, job.Issue{Severity: "warning", Message: "the file is not UTF-8 and has no byte order mark; it was read as Windows-1252, so check any non-ASCII characters in paths and names"})
	}
	return r, nil
}

// stripComment removes a trailing "::" comment. "::" starts a comment at the
// beginning of a line or after white space, outside quotes (a path cannot hold
// "::", so this never cuts a real value).
func stripComment(line string) (string, string) {
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			slashes := 0
			for k := i - 1; k >= 0 && line[k] == '\\'; k-- {
				slashes++
			}
			if slashes%2 == 0 {
				inQuote = !inQuote
			}
		case ':':
			if !inQuote && i+1 < len(line) && line[i+1] == ':' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
				return line[:i], line[i+2:]
			}
		}
	}
	return line, ""
}

type token struct {
	text string
	line int
}

// Parse reads job file text. Hard errors are structural (too large, bad
// quoting, an ambiguous path); everything else is retained and flagged.
func Parse(text string) (Result, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.Contains(text, "\r") {
		return Result{}, errors.New("file contains a bare carriage return")
	}
	lines := strings.Split(text, "\n")
	if len(lines) > MaxLines {
		return Result{}, fmt.Errorf("job file exceeds %d lines", MaxLines)
	}
	var res Result
	note := func(severity, arg, message string) {
		res.Notes = append(res.Notes, job.Issue{Severity: severity, Argument: arg, Message: message})
	}
	var tokens []token
	named := false
	for n, raw := range lines {
		lineNo := n + 1
		if len(raw) > cmdline.MaxCommand {
			return Result{}, fmt.Errorf("line %d is longer than %d bytes", lineNo, cmdline.MaxCommand)
		}
		body, _ := stripComment(raw)
		if strings.TrimSpace(body) == "" {
			trimmed := strings.TrimSpace(raw)
			if value, ok := strings.CutPrefix(strings.TrimSpace(strings.TrimPrefix(trimmed, "::")), NameMarker); ok && strings.HasPrefix(trimmed, "::") && !named {
				named = true
				value = strings.TrimSpace(value)
				if value == "" || len(value) > maxName || !job.TextOK(value) {
					note("warning", "", fmt.Sprintf("line %d: the job name marker is empty, too long or has invalid text; ignored", lineNo))
				} else {
					res.Job.Name = value
				}
			}
			continue
		}
		line := strings.TrimSpace(body)
		upper := strings.ToUpper(line)
		if (strings.HasPrefix(upper, "/SD:") || strings.HasPrefix(upper, "/DD:")) && !strings.HasPrefix(line[4:], `"`) {
			// Robocopy writes these as the rest of the line, spaces included.
			value := line[4:]
			if strings.ContainsAny(value, "\t") || strings.Contains(value, " /") {
				return Result{}, fmt.Errorf("line %d: ambiguous %s value; put quotes around the path or the switch alone on its line", lineNo, line[:3])
			}
			tokens = append(tokens, token{line[:4] + value, lineNo})
			continue
		}
		args, err := cmdline.Tokenize(line)
		if err != nil {
			return Result{}, fmt.Errorf("line %d: %w", lineNo, err)
		}
		for _, a := range args {
			tokens = append(tokens, token{a, lineNo})
		}
	}

	j := &res.Job
	j.Schema = job.Schema
	var haveSD, haveDD, noSD, noDD bool
	mode := ""
	for _, t := range tokens {
		a := t.text
		if !strings.HasPrefix(a, "/") {
			switch mode {
			case "if":
				j.Files = append(j.Files, a)
			case "list":
				j.Options = append(j.Options, a)
			default:
				j.Options = append(j.Options, a)
				note("warning", a, fmt.Sprintf("line %d: stray argument outside any list; kept for review", t.line))
			}
			continue
		}
		mode = ""
		key := job.Key(a)
		switch key {
		case "/SD", "/DD":
			value, ok := strings.CutPrefix(a[len(key):], ":")
			dest := key == "/DD"
			have := &haveSD
			target := &j.Source
			if dest {
				have, target = &haveDD, &j.Destination
			}
			switch {
			case !ok || value == "":
				j.Options = append(j.Options, a)
				note("error", a, fmt.Sprintf("line %d: %s needs a path; kept for review", t.line, key))
			case *have:
				j.Options = append(j.Options, a)
				note("error", a, fmt.Sprintf("line %d: %s appears more than once; the first was used and this one is kept for review", t.line, key))
			default:
				*have, *target = true, value
			}
			continue
		case "/NOSD", "/NODD":
			if a != key {
				j.Options = append(j.Options, a)
				continue
			}
			if key == "/NOSD" {
				noSD = true
			} else {
				noDD = true
			}
			continue
		case "/IF":
			if a != key {
				j.Options = append(j.Options, a)
				continue
			}
			mode = "if"
			continue
		}
		j.Options = append(j.Options, a)
		if o, ok := catalog.Lookup(key); ok {
			if o.Argument == "list" {
				mode = "list"
			}
			if contains(o.Tags, "destructive") {
				note("warning", a, "destructive: a list-only preview and a confirmation are required before it can run, and the current runner refuses it")
			}
			if key == "/QUIT" {
				note("warning", a, "makes Robocopy exit without copying; the runner adds its own /QUIT validation")
			}
		}
	}
	for _, c := range []struct {
		have, no bool
		name     string
	}{{haveSD, noSD, "/SD"}, {haveDD, noDD, "/DD"}} {
		switch {
		case c.have && c.no:
			note("warning", c.name, "the file has both "+c.name+" and its /NO switch; the path was kept")
		case !c.have && !c.no:
			note("warning", c.name, "the file has no "+c.name+"; the job needs it before it can run")
		case !c.have && c.no:
			note("warning", c.name, "the file leaves out "+c.name+" on purpose (/NO switch); set it before the job can run")
		}
	}
	return res, nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// Format renders a job as job file text. The result has the safe defaults the
// runner would add (job.Arguments), so a plain Robocopy /JOB: run keeps them.
// Jobs with errors (unknown or hidden options such as /REG, bad values) are
// refused, as are switches that control the job file itself.
func Format(j job.Job) (string, error) {
	for _, issue := range j.Issues() {
		if issue.Severity == "error" {
			return "", fmt.Errorf("job requires review before export: %s: %s", issue.Argument, issue.Message)
		}
	}
	if j.Name != strings.TrimSpace(j.Name) || len(j.Name) > maxName {
		return "", errors.New("job name must be at most 256 bytes without leading or trailing spaces")
	}
	args := j.Arguments()
	options := args[2+len(j.Files):]
	for _, a := range options {
		switch job.Key(a) {
		case "/IF", "/NOSD", "/NODD", "/QUIT":
			return "", fmt.Errorf("%s controls the job file and cannot be part of an exported job", a)
		}
	}
	var b strings.Builder
	var quoteErr error
	q := func(s string) string {
		out, err := quote(s)
		if err != nil && quoteErr == nil {
			quoteErr = err
		}
		return out
	}
	line := func(indent int, s string) {
		b.WriteString(strings.Repeat("\t", indent) + s + "\r\n")
	}
	line(0, "::")
	if j.Name != "" {
		line(0, ":: "+NameMarker+" "+j.Name)
	}
	line(0, ":: Robocopy job file written by ZTG Copy. It holds no credentials.")
	line(0, ":: Review it before use: /JOB: runs everything below.")
	line(0, "::")
	line(1, "/SD:"+q(j.Source))
	line(1, "/DD:"+q(j.Destination))
	if len(j.Files) != 0 {
		line(1, "/IF")
		for _, f := range j.Files {
			line(2, q(f))
		}
	}
	inList := false
	for _, a := range options {
		if !strings.HasPrefix(a, "/") {
			if !inList {
				return "", fmt.Errorf("%q is not attached to a list option", a)
			}
			line(2, q(a))
			continue
		}
		o, _ := catalog.Lookup(job.Key(a))
		inList = o.Argument == "list"
		if i := strings.Index(a, ":"); i >= 0 {
			line(1, a[:i+1]+q(a[i+1:]))
		} else {
			line(1, a)
		}
	}
	if quoteErr != nil {
		return "", quoteErr
	}
	return b.String(), nil
}

// quote leaves plain text alone and quotes anything that white space, a quote
// or a "::" sequence would change, using the same rules as cmdline.
func quote(s string) (string, error) {
	if s != "" && !strings.ContainsAny(s, " \t\"") && !strings.Contains(s, "::") {
		return s, nil
	}
	return cmdline.Render([]string{s})
}
