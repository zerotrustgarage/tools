package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/zerotrustgarage/tools/copy/internal/catalog"
	"github.com/zerotrustgarage/tools/copy/internal/guard"
	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/private"
	"github.com/zerotrustgarage/tools/copy/internal/rcj"
)

// rcjPlatform is the native open dialog filtered to Robocopy job files. The
// save dialog is the shared SaveFile, which picks its filter from the name.
type rcjPlatform interface {
	OpenRCJ() (string, error)
}

// RCJSwitch is one switch of an imported job file, with the list items that
// follow it. Unknown and hidden switches are listed like any other, flagged.
type RCJSwitch struct {
	Argument    string   `json:"argument"`
	Items       []string `json:"items,omitempty"`
	Known       bool     `json:"known"`
	Hidden      bool     `json:"hidden"`
	Destructive bool     `json:"destructive"`
	Available   bool     `json:"available"`
	Syntax      string   `json:"syntax,omitempty"`
	Help        string   `json:"help,omitempty"`
	Handling    string   `json:"handling,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// RCJImport is the explanation of a job file. Nothing is saved or loaded; the
// renderer shows it and loads View.Job into the designer only after the
// technician confirms.
type RCJImport struct {
	File     string      `json:"file"`
	Encoding string      `json:"encoding"`
	Bytes    int         `json:"bytes"`
	View     View        `json:"view"`
	Notes    []job.Issue `json:"notes"`
	Switches []RCJSwitch `json:"switches"`
	// NeedsReview is true when something is flagged (errors, unknown, hidden
	// or destructive switches); the UI then asks for an explicit acknowledgement.
	NeedsReview bool `json:"needs_review"`
}

// ImportRCJ asks for a job file with the native dialog and explains it. It
// reads, never writes and never runs anything. A cancelled dialog returns nil.
func (a *App) ImportRCJ() (*RCJImport, error) {
	p, err := a.nativePlatform()
	if err != nil {
		return nil, err
	}
	files, ok := p.(rcjPlatform)
	if !ok {
		return nil, errors.New("native file selection is unavailable")
	}
	name, err := files.OpenRCJ()
	if err != nil || name == "" {
		return nil, err
	}
	return explainRCJFile(name)
}

func explainRCJFile(name string) (*RCJImport, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("the job file must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, rcj.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	r, err := rcj.ParseBytes(data)
	if err != nil {
		return nil, err
	}
	view := describe(r.Job)
	result := &RCJImport{File: name, Encoding: r.Encoding, Bytes: len(data), View: view, Notes: r.Notes, Switches: rcjSwitches(r.Job.Options)}
	if result.Notes == nil {
		result.Notes = []job.Issue{}
	}
	for _, issue := range view.Issues {
		if issue.Severity == "error" {
			result.NeedsReview = true
		}
	}
	for _, s := range result.Switches {
		if !s.Known || s.Hidden || s.Destructive {
			result.NeedsReview = true
		}
	}
	return result, nil
}

// rcjSwitches groups options the way the designer does: a list option owns the
// arguments after it, and any other argument that is not a switch stands alone.
func rcjSwitches(options []string) []RCJSwitch {
	result := []RCJSwitch{}
	for i := 0; i < len(options); i++ {
		a := options[i]
		s := RCJSwitch{Argument: a}
		if !strings.HasPrefix(a, "/") {
			result = append(result, s)
			continue
		}
		o, known := catalog.Lookup(job.Key(a))
		if known {
			s.Known, s.Syntax, s.Help, s.Handling, s.Tags = true, o.Syntax, o.Help, o.Handling, o.Tags
			s.Hidden, s.Destructive = hasTag(o.Tags, "hidden"), hasTag(o.Tags, "destructive")
			s.Available = guard.OptionSupported(o.Token)
			if o.Argument == "list" {
				for i+1 < len(options) && !strings.HasPrefix(options[i+1], "/") {
					i++
					s.Items = append(s.Items, options[i])
				}
			}
		}
		result = append(result, s)
	}
	return result
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

// RCJPreview is the text an export would write, shown before a file is chosen.
type RCJPreview struct {
	Text     string `json:"text"`
	Encoding string `json:"encoding"`
	Bytes    int    `json:"bytes"`
}

// RCJExport reports a written file. Check is the command a technician can run
// to have the installed Robocopy read it; Copy never runs Robocopy for this.
type RCJExport struct {
	Path     string `json:"path"`
	Encoding string `json:"encoding"`
	Bytes    int    `json:"bytes"`
	Check    string `json:"check"`
}

const rcjCheckHint = "No credentials are written. ZTG Copy does not run Robocopy for this; to have the installed Robocopy check the file, run robocopy /JOB:FILE /QUIT yourself."

func prepareRCJ(j job.Job, mode string) (string, []byte, error) {
	if mode == "" {
		mode = "auto"
	}
	text, err := rcj.Format(j)
	if err != nil {
		return "", nil, err
	}
	data, err := rcj.Encode(text, mode)
	if err != nil {
		return "", nil, err
	}
	return text, data, nil
}

func rcjEncodingLabel(data []byte, mode string) string {
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return "UTF-16LE with byte order mark"
	case mode == "utf8":
		return "UTF-8 without byte order mark"
	}
	return "ASCII (plain bytes, the same in ANSI and UTF-8)"
}

// PreviewRCJExport applies every export refusal without opening a dialog or
// writing a file, so the renderer can show the text and the refusal reasons.
func (a *App) PreviewRCJExport(j job.Job, encoding string) (RCJPreview, error) {
	text, data, err := prepareRCJ(j, encoding)
	if err != nil {
		return RCJPreview{}, err
	}
	return RCJPreview{Text: text, Encoding: rcjEncodingLabel(data, encoding), Bytes: len(data)}, nil
}

// ExportRCJ writes the designer job as a Robocopy job file. Refusals come
// first, then the native save dialog; the file is created owner-only and an
// existing file is never replaced. A cancelled dialog returns nil.
func (a *App) ExportRCJ(j job.Job, encoding string) (*RCJExport, error) {
	_, data, err := prepareRCJ(j, encoding)
	if err != nil {
		return nil, err
	}
	p, err := a.nativePlatform()
	if err != nil {
		return nil, err
	}
	name, err := p.SaveFile("copy-job.rcj")
	if err != nil || name == "" {
		return nil, err
	}
	if err := private.WriteNewBytes(name, data); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, errors.New("a file with that name already exists; ZTG Copy does not replace files, so choose a new name")
		}
		return nil, err
	}
	return &RCJExport{Path: name, Encoding: rcjEncodingLabel(data, encoding), Bytes: len(data), Check: rcjCheckHint}, nil
}
