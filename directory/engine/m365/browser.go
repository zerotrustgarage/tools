package m365

import (
	"errors"
	"net/url"
	"strings"
)

// checkBrowserURL accepts only an absolute https address for the system
// browser, so DefaultOpenBrowser can never be asked to run a file or another
// URL handler.
func checkBrowserURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil {
		return errors.New("refusing to open a sign-in address that is not https")
	}
	return nil
}
