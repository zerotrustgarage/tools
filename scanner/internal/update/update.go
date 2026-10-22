// Package update checks published ZTG tool releases on explicit user request.
// Canonical source: scripts/updates/update.go. Copies are checked by sync.mjs.
package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const api = "https://api.github.com/repos/zerotrustgarage/tools/releases"
const maxBytes = 4 << 20
const maxPages = 10

var semver = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

type version struct {
	parts [3]int
	pre   string
}

func parse(s string) (version, bool) {
	m := semver.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	v := version{pre: m[4]}
	for i := range v.parts {
		v.parts[i], _ = strconv.Atoi(m[i+1])
	}
	for _, p := range strings.Split(v.pre, ".") {
		if len(p) > 1 && p[0] == '0' && strings.Trim(p, "0123456789") == "" {
			return version{}, false
		}
	}
	return v, true
}

func compare(a, b version) int {
	for i := range a.parts {
		if a.parts[i] < b.parts[i] {
			return -1
		}
		if a.parts[i] > b.parts[i] {
			return 1
		}
	}
	// Only stable release tags are candidates. A prerelease at the same version is older.
	if a.pre != "" && b.pre == "" {
		return -1
	}
	return 0
}

type asset struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Size  int64  `json:"size"`
}

type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Published  string  `json:"published_at"`
	Assets     []asset `json:"assets"`
}

// Result contains only locally constructed URLs and validated version strings.
type Result struct {
	Tool       string `json:"tool"`
	Current    string `json:"current"`
	Latest     string `json:"latest,omitempty"`
	Status     string `json:"status"`
	ReleaseURL string `json:"release_url"`
}

func installer(tool, v string) string {
	switch tool {
	case "scanner":
		return "ZTG-Scanner-" + v + "-x64.msi"
	case "readout":
		return "ZTG-Readout-" + v + "-Setup.exe"
	case "copy":
		return "ZTG-Copy-" + v + "-x64.msi"
	case "tftp":
		return "ZTG-TFTP-" + v + "-x64.msi"
	}
	return ""
}

// Check makes no request until called. It never downloads or executes an installer.
func Check(ctx context.Context, tool, current string) (Result, error) {
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return check(ctx, client, api, tool, current)
}

func check(ctx context.Context, client *http.Client, endpoint, tool, current string) (Result, error) {
	if installer(tool, "0.0.0") == "" {
		return Result{}, errors.New("unknown ZTG tool")
	}
	if len(current) > 128 || strings.ContainsAny(current, "\r\n\x00\x1b") {
		return Result{}, errors.New("invalid build version")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	r := Result{Tool: tool, Current: current, Status: "unreleased", ReleaseURL: "https://github.com/zerotrustgarage/tools/releases?q=" + tool + "-v&expanded=true"}
	var best version
	for page := 1; page <= maxPages; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s?per_page=100&page=%d", endpoint, page), nil)
		if err != nil {
			return Result{}, errors.New("could not prepare update check")
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "ZTG-Tools-Update-Check")
		res, err := client.Do(req)
		if err != nil {
			return Result{}, errors.New("could not check updates; check your connection and try again")
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			if res.StatusCode == 403 || res.StatusCode == 429 {
				return Result{}, errors.New("GitHub rate limit or access restriction; try again later")
			}
			return Result{}, fmt.Errorf("update service returned HTTP %d; try again later", res.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
		res.Body.Close()
		if err != nil || len(data) > maxBytes {
			return Result{}, errors.New("update response is incomplete or too large")
		}
		var releases []release
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 || trimmed[0] != '[' || json.Unmarshal(data, &releases) != nil || len(releases) > 100 {
			return Result{}, errors.New("invalid update response")
		}
		for _, rel := range releases {
			if rel.Draft || rel.Prerelease || rel.Published == "" || !strings.HasPrefix(rel.Tag, tool+"-v") {
				continue
			}
			v := strings.TrimPrefix(rel.Tag, tool+"-v")
			parsed, ok := parse(v)
			if !ok || parsed.pre != "" || strings.Contains(v, "+") {
				continue
			}
			names := make(map[string]bool)
			for _, a := range rel.Assets {
				if a.State == "uploaded" && a.Size > 0 {
					names[a.Name] = true
				}
			}
			if !names["SHA256SUMS.txt"] || !names[installer(tool, v)] {
				continue
			}
			if r.Latest == "" || compare(parsed, best) > 0 {
				r.Latest, best = v, parsed
			}
		}
		if len(releases) < 100 {
			break
		}
		if page == maxPages {
			return Result{}, errors.New("release history limit reached; view GitHub releases directly")
		}
	}
	if r.Latest == "" {
		return r, nil
	}
	r.ReleaseURL = "https://github.com/zerotrustgarage/tools/releases/tag/" + tool + "-v" + r.Latest
	v, ok := parse(current)
	if !ok {
		r.Status = "development"
	} else {
		switch compare(v, best) {
		case -1:
			r.Status = "available"
		case 0:
			r.Status = "current"
		case 1:
			r.Status = "newer"
		}
	}
	return r, nil
}

// Command implements the same explicit check-updates command for each Go CLI.
func Command(ctx context.Context, tool, current string, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("check-updates", flag.ContinueOnError)
	fs.SetOutput(errOut)
	jsonOut := fs.Bool("json", false, "write update status as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("check-updates accepts only --json")
	}
	r, err := Check(ctx, tool, current)
	if err != nil {
		return err
	}
	if *jsonOut {
		return json.NewEncoder(out).Encode(r)
	}
	fmt.Fprintf(out, "ZTG %s: %s (installed %s", tool, r.Status, current)
	if r.Latest != "" {
		fmt.Fprintf(out, ", latest %s", r.Latest)
	}
	fmt.Fprintf(out, ")\n%s\nNo installer was downloaded or run.\n", r.ReleaseURL)
	return nil
}
