//go:build !windows

package engine

import (
	"context"
	"errors"
)

func systemEngine() (string, error) {
	return "", errors.New("Robocopy execution requires Windows")
}

func launch(context.Context, string, []string, string, string, Observer) (Result, error) {
	return Result{}, errors.New("Robocopy execution requires Windows")
}

type unsupportedSys struct{}

func newSys() Sys { return unsupportedSys{} }

func (unsupportedSys) FileVersion(string) (string, error)   { return "", ErrUnsupported }
func (unsupportedSys) Elevation() (ElevationInfo, error)    { return ElevationInfo{}, ErrUnsupported }
func (unsupportedSys) Privileges() (map[string]bool, error) { return nil, ErrUnsupported }
func (unsupportedSys) EmbeddedTrust(string) (uint32, error) { return 0, ErrUnsupported }
func (unsupportedSys) CatalogTrust(string) (CatalogResult, error) {
	return CatalogResult{}, ErrUnsupported
}
