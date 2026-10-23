package bits

import (
	"encoding/json"
	"strings"
	"testing"
)

func sample() Job {
	j := Default()
	j.Transfers = []Transfer{{Source: "https://example.net/file.bin", Destination: `C:\Downloads\file.bin`}}
	return j
}

func TestQuotedPowerShellPreservesLiteralArguments(t *testing.T) {
	j := sample()
	j.Transfers = append(j.Transfers, Transfer{Source: "https://example.net/second.bin", Destination: `C:\Downloads\second.bin`})
	payload := "O'Brien ‘curly’ $(New-Item C:\\injected); $env:TEMP & Unicode 日本語-😀"
	j.Options = append(j.Options, Option{Name: "Description", Values: []string{payload}}, Option{Name: "Credential"})
	view := Describe(j)
	if !view.Valid || !strings.Contains(view.Command, "'O''Brien ‘‘curly’’ $(New-Item") || !strings.Contains(view.Command, "'https://example.net/file.bin', 'https://example.net/second.bin'") {
		t.Fatalf("literal values were changed: %+v", view)
	}
	if !strings.Contains(view.Command, "-Credential (Get-Credential)") || !strings.HasSuffix(view.Command, "-ErrorAction Stop") || !strings.HasPrefix(view.Script, "Import-Module BitsTransfer") {
		t.Fatal(view.Command)
	}
}

func TestInvalidDesignsNeverGenerateCommands(t *testing.T) {
	cases := map[string]func(*Job){
		"schema":              func(j *Job) { j.Schema = 2 },
		"wrong engine":        func(j *Job) { j.Engine = "robocopy" },
		"missing destination": func(j *Job) { j.Transfers[0].Destination = "" },
		"directory":           func(j *Job) { j.Transfers[0].Destination = `C:\Downloads\` },
		"wildcards":           func(j *Job) { j.Transfers[0].Source = "https://example.net/*.bin" },
		"credentials in URL":  func(j *Job) { j.Transfers[0].Source = "https://user:password@example.net/file.bin" },
		"unsupported scheme":  func(j *Job) { j.Transfers[0].Source = "ftp://example.net/file.bin" },
		"duplicate destination": func(j *Job) {
			j.Transfers = append(j.Transfers, Transfer{Source: "https://example.net/other", Destination: `c:\downloads\FILE.bin`})
		},
		"overlap file":        func(j *Job) { j.Transfers[0].Source = `c:\downloads\file.bin` },
		"retry below minimum": func(j *Job) { j.Options = append(j.Options, Option{Name: "RetryInterval", Values: []string{"59"}}) },
		"retry expression": func(j *Job) {
			j.Options = append(j.Options, Option{Name: "RetryInterval", Values: []string{"60; whoami"}})
		},
		"retry overflow": func(j *Job) {
			j.Options = append(j.Options, Option{Name: "RetryTimeout", Values: []string{"2147483648"}})
		},
		"retry relationship": func(j *Job) { j.Options = append(j.Options, Option{Name: "RetryTimeout", Values: []string{"60"}}) },
		"invalid enum":       func(j *Job) { j.Options = append(j.Options, Option{Name: "ProxyUsage", Values: []string{"anything"}}) },
		"duplicate option":   func(j *Job) { j.Options = append(j.Options, Option{Name: "priority", Values: []string{"Low"}}) },
		"unknown option": func(j *Job) {
			j.Options = append(j.Options, Option{Name: "FutureOption", Values: []string{"retained"}})
		},
		"program notification": func(j *Job) {
			j.Options = append(j.Options, Option{Name: "NotifyCmdLine", Values: []string{"powershell.exe"}})
		},
		"certificate bypass": func(j *Job) {
			j.Options = append(j.Options, Option{Name: "SecurityFlags", Values: []string{"IgnoreUnknownCA"}})
		},
		"saved password": func(j *Job) {
			j.Options = append(j.Options, Option{Name: "Credential", Values: []string{"never-store"}})
		},
		"proxy without override": func(j *Job) {
			j.Options = append(j.Options, Option{Name: "ProxyList", Values: []string{"proxy.example.net:8080"}})
		},
		"proxy credentials": func(j *Job) {
			j.Options = append(j.Options, Option{Name: "ProxyUsage", Values: []string{"Override"}}, Option{Name: "ProxyList", Values: []string{"http://user:secret@proxy.example.net:8080"}})
		},
		"description limit": func(j *Job) {
			j.Options = append(j.Options, Option{Name: "Description", Values: []string{strings.Repeat("😀", 513)}})
		},
		"basic HTTP": func(j *Job) {
			j.Transfers[0].Source = "http://example.net/file"
			j.Options = append(j.Options, Option{Name: "Authentication", Values: []string{"Basic"}})
		},
		"multiple uploads": func(j *Job) {
			j.Options[0].Values = []string{"Upload"}
			j.Transfers = []Transfer{{Source: `C:\one.bin`, Destination: "https://example.net/one"}, {Source: `C:\two.bin`, Destination: "https://example.net/two"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			j := sample()
			mutate(&j)
			view := Describe(j)
			if view.Valid || view.Command != "" || view.Script != "" || len(view.Issues) == 0 {
				t.Fatalf("invalid design generated a command: %+v", view)
			}
			if _, err := Command(j); err == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
}

func TestValidBitsModesAndProxyRelationships(t *testing.T) {
	for _, kind := range []string{"Download", "Upload", "UploadReply"} {
		j := sample()
		j.Options[0].Values = []string{kind}
		if kind != "Download" {
			j.Transfers = []Transfer{{Source: `C:\Upload\file.bin`, Destination: "https://example.net/file.bin"}}
		}
		j.Options = append(j.Options, Option{Name: "ProxyUsage", Values: []string{"Override"}}, Option{Name: "ProxyList", Values: []string{"proxy.example.net:8080", "http://proxy2.example.net:8080"}}, Option{Name: "ProxyBypass", Values: []string{"*.example.net", "<local>"}}, Option{Name: "RetryTimeout", Values: []string{"0"}})
		if view := Describe(j); !view.Valid {
			t.Fatalf("%s: %+v", kind, view.Issues)
		}
	}
}

func TestStrictImportAndUnknownParameterRetention(t *testing.T) {
	for _, document := range []string{
		`{"schema":1,"engine":"bits","password":"secret"}`,
		`{"schema":1,"engine":"bits","transfers":[],"options":[{"name":"Credential","password":"secret"}]}`,
		`{"schema":1,"engine":"robocopy"}`,
		`{"schema":2,"engine":"bits"}`,
	} {
		if _, err := Decode(strings.NewReader(document)); err == nil {
			t.Fatal("invalid document accepted")
		}
	}
	j := sample()
	j.Options = append(j.Options, Option{Name: "FutureOption", Values: []string{"kept"}})
	data, _ := json.Marshal(j)
	parsed, err := Decode(strings.NewReader(string(data)))
	if err != nil || parsed.Options[len(parsed.Options)-1].Values[0] != "kept" || Describe(parsed).Valid {
		t.Fatalf("unknown parameter lost or accepted: %+v, %v", parsed, err)
	}
}

func TestCatalogIsBoundedAndContainsNoSecretValues(t *testing.T) {
	seen := map[string]bool{}
	for _, entry := range Catalog() {
		if entry.Name == "" || seen[strings.ToLower(entry.Name)] || entry.Help == "" {
			t.Fatalf("invalid catalog entry: %+v", entry)
		}
		seen[strings.ToLower(entry.Name)] = true
		if entry.Kind == "enum" && len(entry.Values) == 0 {
			t.Fatal("enum without choices")
		}
	}
	for _, name := range []string{"source", "destination", "asynchronous", "transferpolicy", "retryinterval", "credential"} {
		if !seen[name] {
			t.Fatal("missing parameter", name)
		}
	}
}
