// Package template expands placeholders into individual arguments, never shell text.
package template

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/private"
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
var fieldName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}// Package template expands placeholders into individual arguments, never shell text.
package template

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/private"
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)
var placeholder = regexp.MustCompile(`\{([a-z][a-z0-9_-]{0,63})\}`)

type Field struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
}

type Template struct {
	Schema         int      `json:"schema"`
	CatalogVersion int      `json:"catalog_version"`
	ID             string   `json:"id"`
	Description    string   `json:"description,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Fields         []Field  `json:"fields,omitempty"`
	Job            job.Job  `json:"job"`
}

func (t Template) Validate() error {
	if t.Schema != 1 || t.CatalogVersion != 1 || t.Job.Schema != job.Schema || !idPattern.MatchString(t.ID) {
		return errors.New("unsupported template schema/catalog or invalid id (use 1 to 64 lowercase letters, digits and hyphens)")
	}
	names := map[string]bool{"date": true, "time": true, "computer": true, "user": true}
	for _, f := range t.Fields {
		if !fieldName.MatchString(f.Name) || names[f.Name] || strings.Contains(f.Name, "password") || strings.Contains(f.Name, "credential") || f.Name == "secret" || f.Name == "token" {
			return fmt.Errorf("invalid, reserved or duplicate field %q", f.Name)
		}
		if f.Kind != "path" && f.Kind != "number" && f.Kind != "text" {
			return fmt.Errorf("invalid kind for field %s", f.Name)
		}
		names[f.Name] = true
	}
	for _, a := range t.arguments() {
		for _, m := range placeholder.FindAllStringSubmatch(a, -1) {
			if !names[m[1]] {
				return fmt.Errorf("undefined field %s", m[1])
			}
		}
		rest := placeholder.ReplaceAllString(a, "")
		if strings.ContainsAny(rest, "{}") {
			return errors.New("invalid placeholder syntax")
		}
	}
	return nil
}

func (t Template) arguments() []string {
	args := []string{t.Job.Name, t.Job.Source, t.Job.Destination}
	args = append(args, t.Job.Files...)
	return append(args, t.Job.Options...)
}

func (t Template) Resolve(values map[string]string, now time.Time, computer, user string) (job.Job, error) {
	if err := t.Validate(); err != nil {
		return job.Job{}, err
	}
	filled := map[string]string{"date": now.Format("2006-01-02"), "time": now.Format("150405"), "computer": computer, "user": user}
	fields := map[string]Field{}
	for _, f := range t.Fields {
		fields[f.Name] = f
		filled[f.Name] = f.Default
	}
	for k, v := range values {
		if _, ok := fields[k]; !ok {
			return job.Job{}, fmt.Errorf("unknown field %s", k)
		}
		filled[k] = v
	}
	for _, a := range t.arguments() {
		for _, m := range placeholder.FindAllStringSubmatch(a, -1) {
			v := filled[m[1]]
			if v == "" {
				return job.Job{}, fmt.Errorf("unfilled field %s", m[1])
			}
			if strings.ContainsAny(v, "\x00\r\n{}") {
				return job.Job{}, fmt.Errorf("invalid value for %s", m[1])
			}
			if field, ok := fields[m[1]]; ok {
				if field.Kind == "path" && !job.AbsoluteWindowsPath(v) {
					return job.Job{}, fmt.Errorf("field %s requires an absolute Windows path", m[1])
				}
				if field.Kind == "number" && strings.Trim(v, "0123456789") != "" {
					return job.Job{}, fmt.Errorf("field %s requires decimal digits", m[1])
				}
			}
		}
	}
	expand := func(s string) string {
		return placeholder.ReplaceAllStringFunc(s, func(m string) string { return filled[m[1:len(m)-1]] })
	}
	j := t.Job
	j.Name, j.Source, j.Destination = expand(j.Name), expand(j.Source), expand(j.Destination)
	j.Files = append([]string(nil), j.Files...)
	j.Options = append([]string(nil), j.Options...)
	for i := range j.Files {
		j.Files[i] = expand(j.Files[i])
	}
	for i, a := range j.Options {
		j.Options[i] = expand(a)
		if (strings.HasPrefix(a, "/") && job.Key(a) != job.Key(j.Options[i])) || (!strings.HasPrefix(a, "/") && strings.HasPrefix(j.Options[i], "/")) {
			return job.Job{}, errors.New("a placeholder cannot add or change an option token")
		}
	}
	for _, issue := range j.Issues() {
		if issue.Severity == "error" {
			return job.Job{}, fmt.Errorf("%s: %s", issue.Argument, issue.Message)
		}
	}
	return j, nil
}

func DefaultDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ZTG", "Copy", "templates"), nil
}

func Load(dir, id string) (Template, error) {
	if !idPattern.MatchString(id) {
		return Template{}, errors.New("invalid template id")
	}
	name := filepath.Join(dir, id+".json")
	info, err := os.Lstat(name)
	if err != nil {
		return Template{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Template{}, errors.New("template must be a regular file")
	}
	f, err := os.Open(name)
	if err != nil {
		return Template{}, err
	}
	defer f.Close()
	var t Template
	if err := job.DecodeStrict(f, &t); err != nil {
		return t, err
	}
	if t.ID != id {
		return t, errors.New("template id disagrees with its filename")
	}
	return t, t.Validate()
}

func Save(dir string, t Template) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if err := private.EnsureDir(dir); err != nil {
		return err
	}
	return private.WriteNew(filepath.Join(dir, t.ID+".json"), t)
}

func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") && idPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
