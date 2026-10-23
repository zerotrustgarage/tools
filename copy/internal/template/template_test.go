package template

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/cmdline"
	"github.com/zerotrustgarage/tools/copy/internal/job"
)

func TestSubstitutionIsOneArgument(t *testing.T) {
	tmpl := Template{Schema: 1, CatalogVersion: 1, ID: "nas-copy", Fields: []Field{{Name: "source", Kind: "path"}, {Name: "client", Kind: "text"}}, Job: job.Job{Schema: 1, Source: "{source}", Destination: `\\nas\share\{client}`, Options: []string{"/R:2"}}}
	j, err := tmpl.Resolve(map[string]string{"source": `C:\data space\`, "client": "Acme /MIR"}, time.Now(), "pc", "tech")
	if err != nil {
		t.Fatal(err)
	}
	command, err := j.Command()
	if err != nil {
		t.Fatal(err)
	}
	got, err := cmdline.Tokenize(command)
	if err != nil || !reflect.DeepEqual(got[1:], j.Arguments()) {
		t.Fatalf("argument boundary changed: %q %v", got, err)
	}
	if len(j.Options) != 1 {
		t.Fatal("placeholder added an option")
	}
	tmpl.Job.Options = []string{"/R:{client}"}
	if _, err := tmpl.Resolve(map[string]string{"source": `C:\a`, "client": "2 /MIR"}, time.Now(), "", ""); err == nil {
		t.Fatal("accepted injected numeric value")
	}
}

func TestLibraryAndStrictImport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	tmpl := Template{Schema: 1, CatalogVersion: 1, ID: "copy", Job: job.Job{Schema: 1, Source: `C:\a`, Destination: `D:\b`}}
	if err := Save(dir, tmpl); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, tmpl); err == nil {
		t.Fatal("overwrote existing template")
	}
	got, err := Load(dir, "copy")
	if err != nil || got.Job.Source != tmpl.Job.Source {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := Load(dir, "../copy"); err == nil {
		t.Fatal("accepted traversal")
	}
	for _, doc := range []string{`{"schema":1,"password":"no"}`, `{"schema":1,"job":{"password":"no"}}`} {
		if err := job.DecodeStrict(strings.NewReader(doc), &Template{}); err == nil {
			t.Fatal("accepted password field")
		}
	}
}

func FuzzTemplateReader(f *testing.F) {
	f.Add(`{"schema":1,"catalog_version":1,"id":"copy","job":{"schema":1,"source":"C:\\a","destination":"D:\\b"}}`)
	f.Fuzz(func(t *testing.T, data string) {
		var tmpl Template
		if job.DecodeStrict(strings.NewReader(data), &tmpl) != nil {
			return
		}
		if tmpl.Validate() == nil {
			_, _ = tmpl.Resolve(nil, time.Unix(0, 0), "pc", "tech")
		}
	})
}
