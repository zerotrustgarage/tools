//go:build !windows

package spike

import (
	"context"
	"errors"
)

func Run(context.Context, string) error {
	return errors.New("RC0 captures require real Windows and the in-box Robocopy; unit tests run on Linux")
}
