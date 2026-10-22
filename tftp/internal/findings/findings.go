// Package findings reports configuration hygiene without returning secret values.
package findings

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

const MaxBytes = 16 << 20
const MaxLine = 64 << 10
const MaxFindings = 1000

type Finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Line     int    `json:"line"`
	Verify   bool   `json:"verify,omitempty"`
}
type Result struct {
	Findings  []Finding `json:"findings"`
	Truncated bool      `json:"truncated,omitempty"`
	Skipped   string    `json:"skipped,omitempty"`
}

func Scan(input io.Reader) (Result, error) {
	result := Result{Findings: []Finding{}}
	r := bufio.NewReaderSize(io.LimitReader(input, MaxBytes+1), MaxLine)
	lineNo, total := 0, 0
	vty, noVstack := false, false
	switchLine := 0
	add := func(code, severity string, line int, verify bool) {
		if len(result.Findings) < MaxFindings {
			result.Findings = append(result.Findings, Finding{code, severity, line, verify})
		} else {
			result.Truncated = true
		}
	}
	for {
		line, e := r.ReadSlice('\n')
		total += len(line)
		if bytes.IndexByte(line, 0) >= 0 {
			return Result{Findings: []Finding{}, Skipped: "binary"}, nil
		}
		if total > MaxBytes {
			result.Truncated = true
			break
		}
		lineNo++
		if e == bufio.ErrBufferFull {
			result.Truncated = true
			for e == bufio.ErrBufferFull {
				line, e = r.ReadSlice('\n')
				total += len(line)
				if bytes.IndexByte(line, 0) >= 0 {
					return Result{Findings: []Finding{}, Skipped: "binary"}, nil
				}
				if total > MaxBytes {
					return result, nil
				}
			}
			if e != nil && e != io.EOF {
				return result, errors.New("configuration read failed")
			}
			if e == io.EOF {
				break
			}
			continue
		}
		if e != nil && e != io.EOF {
			return result, errors.New("configuration read failed")
		}
		s := strings.TrimSpace(string(line))
		f := strings.Fields(strings.ToLower(s))
		if len(f) > 0 {
			if f[0] == "!" || strings.HasPrefix(f[0], "!") {
				vty = false
			} else {
				if !strings.HasPrefix(string(line), " ") && !strings.HasPrefix(string(line), "\t") {
					vty = len(f) >= 2 && f[0] == "line" && f[1] == "vty"
				}
				if len(f) == 2 && f[0] == "no" && f[1] == "vstack" {
					noVstack = true
				}
				if len(f) >= 3 && f[0] == "switch" {
					for _, word := range f[1:] {
						if word == "provision" && switchLine == 0 {
							switchLine = lineNo
						}
					}
				}
				if len(f) >= 3 && f[0] == "snmp-server" && f[1] == "community" {
					add("snmp-community", "medium", lineNo, false)
				}
				if len(f) == 3 && f[0] == "ip" && f[1] == "http" && f[2] == "server" {
					add("http-cleartext", "medium", lineNo, false)
				}
				if vty && len(f) >= 3 && f[0] == "transport" && f[1] == "input" {
					for _, word := range f[2:] {
						if word == "telnet" || word == "all" {
							add("telnet", "medium", lineNo, false)
							break
						}
					}
				}
				// Each line reports each password rule at most once. Only syntax
				// tokens are inspected; neither tokens nor the source line escape.
				seen := map[string]bool{}
				for i, word := range f {
					if word != "password" && word != "secret" && word != "key" && word != "key-string" {
						continue
					}
					if i+1 >= len(f) || f[0] == "no" {
						continue
					}
					kind := f[i+1]
					code := ""
					severity := "high"
					if kind == "7" {
						code = "reversible-password"
					} else if kind == "5" {
						code = "weak-hash"
						severity = "low"
					} else if kind == "0" {
						code = "cleartext-password"
					} else if (word == "password" || word == "secret") && kind != "4" && kind != "6" && kind != "8" && kind != "9" {
						code = "cleartext-password"
					}
					if code != "" && !seen[code] {
						add(code, severity, lineNo, false)
						seen[code] = true
					}
				}
			}
		}
		if e == io.EOF {
			break
		}
	}
	if switchLine > 0 && !noVstack {
		add("smart-install", "high", switchLine, true)
	}
	return result, nil
}

func (r Result) Codes() []string {
	out := []string{}
	seen := map[string]bool{}
	for _, f := range r.Findings {
		if !seen[f.Code] {
			seen[f.Code] = true
			out = append(out, f.Code)
		}
	}
	return out
}
