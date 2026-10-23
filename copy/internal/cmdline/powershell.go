package cmdline

import "strings"

// PowerShellLiteral preserves data without expanding expressions or variables.
// PowerShell treats all four typographic single quotes as string delimiters.
func PowerShellLiteral(value string) string {
	return "'" + strings.NewReplacer("'", "''", "‘", "‘‘", "’", "’’", "‚", "‚‚", "‛", "‛‛").Replace(value) + "'"
}
