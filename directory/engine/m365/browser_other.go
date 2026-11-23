//go:build !windows

package m365

import "errors"

// DefaultOpenBrowser is available on Windows only; elsewhere the caller
// passes SessionOptions.OpenBrowser.
func DefaultOpenBrowser(rawURL string) error {
	if err := checkBrowserURL(rawURL); err != nil {
		return err
	}
	return errors.New("no default browser opener on this platform; set SessionOptions.OpenBrowser")
}
