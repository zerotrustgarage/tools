// Package cmdline imports and renders direct Windows process arguments.
// It never executes a shell or expands environment variables.
package cmdline

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const MaxCommand = 32766

func Tokenize(command string) ([]string, error) {
	if !utf8.ValidString(command) || strings.ContainsAny(command, "\x00\r\n") || len(command) > MaxCommand {
		return nil, errors.New("invalid or oversized Windows command line")
	}
	var args []string
	for i := 0; i < len(command); {
		for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
			i++
		}
		if i == len(command) {
			break
		}
		var b strings.Builder
		quoted := false
		for i < len(command) {
			if !quoted && (command[i] == ' ' || command[i] == '\t') {
				break
			}
			slashes := 0
			for i < len(command) && command[i] == '\\' {
				slashes++
				i++
			}
			if i < len(command) && command[i] == '"' {
				b.WriteString(strings.Repeat("\\", slashes/2))
				if slashes%2 == 1 {
					b.WriteByte('"')
				} else if quoted && i+1 < len(command) && command[i+1] == '"' {
					b.WriteByte('"')
					i++
				} else {
					quoted = !quoted
				}
				i++
			} else {
				b.WriteString(strings.Repeat("\\", slashes))
				if i == len(command) || !quoted && (command[i] == ' ' || command[i] == '\t') {
					break
				}
				b.WriteByte(command[i])
				i++
			}
		}
		if quoted {
			return nil, errors.New("unclosed quote in Windows command line")
		}
		args = append(args, b.String())
	}
	return args, nil
}

// Render quotes every argument for direct CreateProcess invocation. This string
// is not a cmd.exe or PowerShell script; neither shell is involved in a run.
func Render(args []string) (string, error) {
	var result []string
	for _, arg := range args {
		if !utf8.ValidString(arg) || strings.ContainsFunc(arg, func(r rune) bool { return r < 32 || r == 127 }) {
			return "", errors.New("argument contains invalid text")
		}
		var b strings.Builder
		b.WriteByte('"')
		slashes := 0
		for _, c := range arg {
			if c == '\\' {
				slashes++
				continue
			}
			if c == '"' {
				b.WriteString(strings.Repeat("\\", slashes*2+1))
			} else {
				b.WriteString(strings.Repeat("\\", slashes))
			}
			slashes = 0
			b.WriteRune(c)
		}
		b.WriteString(strings.Repeat("\\", slashes*2))
		b.WriteByte('"')
		result = append(result, b.String())
	}
	command := strings.Join(result, " ")
	if len(command) > MaxCommand {
		return "", errors.New("Windows command line is too long")
	}
	return command, nil
}
