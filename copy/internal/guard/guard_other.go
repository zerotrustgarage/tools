//go:build !windows

package guard

import (
	"context"
	"errors"

	"github.com/zerotrustgarage/tools/copy/internal/job"
)

func inspect(context.Context, job.Job, string) (Facts, error) {
	return Facts{}, errors.New("filesystem probes and Robocopy runs require Windows")
}
