// Package guard fails closed before an engine invocation.
package guard

import (
	"context"
	"fmt"
	"strings"

	"github.com/zerotrustgarage/tools/copy/internal/job"
)

// The first runner supports ordinary local copies only. Destructive, privileged,
// monitor, external-job and output-control switches require later safety slices.
var allowed = map[string]bool{}

func init() {
	for _, token := range strings.Fields("/E /S /LEV /Z /J /MT /COPY /DCOPY /R /W /XJ /FFT /DST /MAX /MIN /MAXAGE /MINAGE /MAXLAD /MINLAD /XF /XD /XO /XN /XC /XX /XL /IM /IS /IT /IA /XA /NOOFFLOAD /NOCLONE /COMPRESS /IORATE /IOMAXSIZE /THRESHOLD") {
		allowed[token] = true
	}
}

type Facts struct {
	SourceID             string `json:"source_id"`
	DestinationID        string `json:"destination_id"`
	SourceCanonical      string `json:"source_canonical"`
	DestinationCanonical string `json:"destination_canonical"`
	FreeBytes            uint64 `json:"free_bytes"`
	EntriesChecked       int64  `json:"entries_checked"`
}

// OptionSupported reports the current execution subset for designers.
// Value checks and all filesystem guards still apply at execution.
func OptionSupported(token string) bool { return allowed[job.Key(token)] }

func Policy(j job.Job) error {
	for _, endpoint := range []string{j.Source, j.Destination} {
		if strings.HasPrefix(endpoint, `\\`) {
			return fmt.Errorf("the initial runner supports local fixed drives; network copies are not enabled yet")
		}
	}
	for _, issue := range j.Issues() {
		if issue.Severity == "error" {
			return fmt.Errorf("%s: %s", issue.Argument, issue.Message)
		}
	}
	for i := 0; i < len(j.Options); i++ {
		a := j.Options[i]
		key := job.Key(a)
		if !allowed[key] {
			return fmt.Errorf("%s is not enabled in the initial copy runner; it remains available for explanation", key)
		}
		if key == "/COPY" || key == "/DCOPY" {
			value := strings.SplitN(strings.ToUpper(a), ":", 2)
			if len(value) != 2 || value[1] != "DAT" {
				return fmt.Errorf("%s currently supports DAT only", key)
			}
		}
		if key == "/XD" || key == "/XF" {
			for i+1 < len(j.Options) && !strings.HasPrefix(j.Options[i+1], "/") {
				i++
			}
		}
	}
	return nil
}

func Overlap(a, b string) bool {
	a = strings.ToUpper(strings.TrimRight(a, "\\/"))
	b = strings.ToUpper(strings.TrimRight(b, "\\/"))
	return a == b || strings.HasPrefix(a, b+"\\") || strings.HasPrefix(b, a+"\\")
}

func Inspect(j job.Job, records string) (Facts, error) {
	return InspectContext(context.Background(), j, records)
}

func InspectContext(ctx context.Context, j job.Job, records string) (Facts, error) {
	if err := Policy(j); err != nil {
		return Facts{}, err
	}
	return inspect(ctx, j, records)
}
