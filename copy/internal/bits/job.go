package bits

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/zerotrustgarage/tools/copy/internal/job"
)

const Schema = 1
const MaxFiles = 200

type Transfer struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

type Option struct {
	Name   string   `json:"name"`
	Values []string `json:"values,omitempty"`
}

type Job struct {
	Schema    int        `json:"schema"`
	Engine    string     `json:"engine"`
	Transfers []Transfer `json:"transfers"`
	Options   []Option   `json:"options"`
}

type View struct {
	Job     Job         `json:"job"`
	Valid   bool        `json:"valid"`
	Command string      `json:"command"`
	Script  string      `json:"script"`
	Issues  []job.Issue `json:"issues"`
}

func Default() Job {
	return Job{Schema: Schema, Engine: "bits", Transfers: []Transfer{}, Options: []Option{{Name: "TransferType", Values: []string{"Download"}}, {Name: "Priority", Values: []string{"Foreground"}}}}
}

func Decode(r io.Reader) (Job, error) {
	var j Job
	if err := job.DecodeStrict(r, &j); err != nil {
		return j, err
	}
	if j.Schema != Schema || j.Engine != "bits" {
		return j, errors.New("expected a schema 1 BITS job")
	}
	return j, nil
}

func textOK(s string) bool {
	return len(s) <= 32768 && utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return r < 32 || r == 127 })
}

func filePath(s string) bool {
	if !job.AbsoluteWindowsPath(s) || strings.ContainsAny(s, "[]") || strings.HasSuffix(s, "\\") || strings.HasSuffix(s, "/") {
		return false
	}
	if strings.HasPrefix(s, `\\`) {
		return len(strings.Split(s[2:], "\\")) >= 3
	}
	return len(s) > 3
}

func webURL(s string) (*url.URL, bool) {
	u, err := url.Parse(s)
	if err != nil || !textOK(s) || strings.ContainsAny(s, " \\") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path == "" || strings.HasSuffix(u.Path, "/") || strings.ContainsAny(u.Path, "*[]") {
		return u, false
	}
	return u, true
}

var bypassPattern = regexp.MustCompile(`^([a-zA-Z0-9*_.-]+|<local>)$`)

func proxyOK(s string) bool {
	u, err := url.Parse(s)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return u.Hostname() != "" && u.User == nil && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(s, " \\")
	}
	host, port, err := net.SplitHostPort(s)
	n, numberErr := strconv.Atoi(port)
	return err == nil && host != "" && numberErr == nil && n > 0 && n <= 65535 && !strings.ContainsAny(host, " /\\@")
}

func (j Job) Issues() []job.Issue {
	issues := []job.Issue{}
	add := func(severity, argument, message string) {
		issues = append(issues, job.Issue{Severity: severity, Argument: argument, Message: message})
	}
	errorAt := func(argument, message string) { add("error", argument, message) }
	if j.Schema != Schema || j.Engine != "bits" {
		errorAt("job", "expected a schema 1 BITS job")
	}
	if len(j.Transfers) == 0 || len(j.Transfers) > MaxFiles {
		errorAt("files", "provide between 1 and 200 source/destination file pairs")
	}
	if len(j.Options) > 64 {
		errorAt("options", "too many options")
		return issues
	}
	selected := map[string]Option{}
	for _, option := range j.Options {
		entry, known := lookup(option.Name)
		if !known {
			errorAt(option.Name, "unknown BITS parameter; retained for review")
			continue
		}
		name := entry.Name
		if _, duplicate := selected[name]; duplicate {
			errorAt(name, "parameter is selected more than once")
		}
		selected[name] = option
		if !entry.Available {
			errorAt(name, "reference only: "+entry.Help)
			continue
		}
		switch entry.Kind {
		case "switch", "prompt":
			if len(option.Values) != 0 {
				errorAt(name, "this option takes no saved value")
			}
		case "list":
			if len(option.Values) == 0 || len(option.Values) > MaxFiles {
				errorAt(name, "provide between 1 and 200 values")
			}
		default:
			if len(option.Values) != 1 {
				errorAt(name, "provide exactly one value")
			}
		}
		for _, value := range option.Values {
			if value == "" || !textOK(value) {
				errorAt(name, "values must be nonempty text without control characters")
			}
			switch entry.Kind {
			case "enum":
				found := false
				for _, choice := range entry.Values {
					found = found || strings.EqualFold(choice, value)
				}
				if !found {
					errorAt(name, "choose one of: "+strings.Join(entry.Values, ", "))
				}
			case "integer":
				minimum := int64(0)
				if name == "RetryInterval" {
					minimum = 60
				}
				n, err := strconv.ParseInt(value, 10, 32)
				if err != nil || n < minimum || !digits(value) {
					errorAt(name, fmt.Sprintf("use an integer from %d to 2147483647", minimum))
				}
			}
			if name == "Description" && len(utf16.Encode([]rune(value))) > 1024 {
				errorAt(name, "description exceeds 1,024 UTF-16 characters")
			}
			if name == "ProxyList" && !proxyOK(value) {
				errorAt(name, "use a proxy URI without credentials, or host:port")
			}
			if name == "ProxyBypass" && !bypassPattern.MatchString(value) {
				errorAt(name, "use a host pattern such as *.example.net or <local>")
			}
		}
	}
	value := func(name, fallback string) string {
		if option, ok := selected[name]; ok && len(option.Values) == 1 {
			return option.Values[0]
		}
		return fallback
	}
	kind := value("TransferType", "Download")
	upload := strings.EqualFold(kind, "Upload") || strings.EqualFold(kind, "UploadReply")
	if upload && len(j.Transfers) != 1 {
		errorAt("files", "BITS uploads require exactly one file pair; use a separate job for each file")
	}
	destinations := map[string]bool{}
	for i, transfer := range j.Transfers {
		label := fmt.Sprintf("file %d", i+1)
		sourceURL, sourceWeb := webURL(transfer.Source)
		_, destinationWeb := webURL(transfer.Destination)
		if upload {
			if !filePath(transfer.Source) || strings.HasPrefix(transfer.Source, `\\`) {
				errorAt(label+" source", "an upload source must be an absolute local file path")
			}
			if !destinationWeb {
				errorAt(label+" destination", "an upload destination must be an HTTP or HTTPS file URL without embedded credentials")
			}
		} else {
			if !sourceWeb && !filePath(transfer.Source) {
				errorAt(label+" source", "use an HTTP/HTTPS file URL or an absolute local/UNC file path without wildcards")
			}
			if !filePath(transfer.Destination) {
				errorAt(label+" destination", "use an absolute local or UNC file path, including its filename")
			}
		}
		canonical := strings.ToLower(path.Clean(strings.ReplaceAll(transfer.Destination, "\\", "/")))
		if destinations[canonical] {
			errorAt(label+" destination", "each destination must be unique")
		}
		destinations[canonical] = true
		if filePath(transfer.Source) && strings.EqualFold(path.Clean(strings.ReplaceAll(transfer.Source, "\\", "/")), canonical) {
			errorAt(label, "source and destination must differ")
		}
		if strings.EqualFold(value("Authentication", ""), "Basic") && sourceWeb && sourceURL.Scheme != "https" {
			errorAt("Authentication", "Basic authentication requires an HTTPS source")
		}
		if upload && strings.EqualFold(value("Authentication", ""), "Basic") {
			u, ok := webURL(transfer.Destination)
			if ok && u.Scheme != "https" {
				errorAt("Authentication", "Basic authentication requires an HTTPS destination")
			}
		}
		if sourceWeb && sourceURL.Scheme == "http" || destinationWeb && strings.HasPrefix(transfer.Destination, "http:") {
			add("warning", label, "HTTP does not provide TLS transport protection")
		}
	}
	for _, name := range []string{"ProxyList", "ProxyBypass"} {
		if _, exists := selected[name]; exists && !strings.EqualFold(value("ProxyUsage", "SystemDefault"), "Override") {
			errorAt(name, "requires ProxyUsage Override")
		}
	}
	if strings.EqualFold(value("ProxyUsage", ""), "Override") && len(selected["ProxyList"].Values) == 0 && len(selected["ProxyBypass"].Values) == 0 {
		errorAt("ProxyUsage", "Override requires a proxy list or bypass list")
	}
	interval, intervalErr := strconv.ParseInt(value("RetryInterval", "600"), 10, 32)
	timeout, timeoutErr := strconv.ParseInt(value("RetryTimeout", "1209600"), 10, 32)
	if intervalErr == nil && timeoutErr == nil && timeout > 0 && interval > timeout {
		errorAt("RetryTimeout", "must be zero to disable retries, or at least the retry interval")
	}
	if _, exists := selected["Asynchronous"]; exists {
		add("warning", "Asynchronous", "Run the command in PowerShell to create a persistent BITS job. Wait for Transferred before using Complete-BitsTransfer")
	}
	if _, exists := selected["Suspended"]; exists {
		add("warning", "Suspended", "The job stays paused until Resume-BitsTransfer is called in PowerShell")
	}
	if upload {
		add("warning", "TransferType", "The HTTP server must support BITS uploads; an ordinary web upload endpoint is insufficient")
	}
	if strings.EqualFold(value("TransferPolicy", ""), "Always") {
		add("warning", "TransferPolicy", "This policy permits metered and roaming networks")
	}
	return issues
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func Describe(j Job) View {
	view := View{Job: j, Issues: j.Issues(), Valid: true}
	for _, issue := range view.Issues {
		if issue.Severity == "error" {
			view.Valid = false
		}
	}
	if !view.Valid {
		return view
	}
	sources, destinations := []string{}, []string{}
	for _, transfer := range j.Transfers {
		sources, destinations = append(sources, quote(transfer.Source)), append(destinations, quote(transfer.Destination))
	}
	parts := []string{`BitsTransfer\Start-BitsTransfer`, "-Source", strings.Join(sources, ", "), "-Destination", strings.Join(destinations, ", ")}
	for _, option := range j.Options {
		entry, _ := lookup(option.Name)
		parts = append(parts, "-"+entry.Name)
		if entry.Kind == "prompt" {
			parts = append(parts, "(Get-Credential)")
		} else if len(option.Values) != 0 {
			values := []string{}
			for _, value := range option.Values {
				values = append(values, quote(value))
			}
			parts = append(parts, strings.Join(values, ", "))
		}
	}
	parts = append(parts, "-ErrorAction", "Stop")
	view.Command = strings.Join(parts, " ")
	view.Script = "Import-Module BitsTransfer -ErrorAction Stop\n" + view.Command + "\n"
	return view
}

func Command(j Job) (string, error) {
	view := Describe(j)
	if !view.Valid {
		for _, issue := range view.Issues {
			if issue.Severity == "error" {
				return "", fmt.Errorf("%s: %s", issue.Argument, issue.Message)
			}
		}
	}
	return view.Command, nil
}
