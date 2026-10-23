//go:build !windows

package engine

import (
	"context"
	"errors"
)

func systemEngine() (string, error) {
	return "", errors.New("Robocopy execution requires Windows")
}

func launch(context.Context, string, []string, string, string) (Result, error) {
	return Result{}, errors.New("Robocopy execution requires Windows")
}
