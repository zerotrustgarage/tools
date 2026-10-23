package spike

import (
	"errors"
	"regexp"
	"strconv"
)

// fixtureCounts is an RC0 assertion helper for controlled /BYTES native logs,
// not the production parser. Ambiguous tables are rejected. Labels are ignored:
// the three rows are directories, files and bytes; columns are total, copied,
// skipped, mismatch, failed and extra.
type fixtureCounts [3][6]uint64

var numericRow = regexp.MustCompile(`(?m)^[^:\r\n]+:[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]*\r?$`)

func fixtureSummary(text string) (fixtureCounts, error) {
	var result fixtureCounts
	rows := numericRow.FindAllStringSubmatch(text, -1)
	if len(rows) != 3 {
		return result, errors.New("expected exactly three unambiguous numeric summary rows")
	}
	for i, row := range rows {
		for j, number := range row[1:] {
			value, err := strconv.ParseUint(number, 10, 64)
			if err != nil {
				return result, err
			}
			result[i][j] = value
		}
	}
	return result, nil
}
