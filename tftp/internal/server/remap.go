package server

import (
	"encoding/hex"
	"errors"
	"github.com/zerotrustgarage/tools/tftp/internal/store"
	"strings"
)

type Remap struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Template maps a requested filename containing a 12-hex-digit MAC to a concrete
// per-device file. It never infers MAC identity from a source IP or renders secrets.
type Template struct {
	Request string `json:"request"`
	File    string `json:"file"`
}

func ValidateRules(remap []Remap, templates []Template) error {
	if len(remap) > 128 || len(templates) > 32 {
		return errors.New("too many provisioning rules")
	}
	seen := map[string]bool{}
	for _, r := range remap {
		a, e := store.Name(r.From)
		if e != nil || a != r.From {
			return errors.New("invalid remap source")
		}
		b, e := store.Name(r.To)
		if e != nil || b != r.To || seen[a] {
			return errors.New("invalid or duplicate remap")
		}
		seen[a] = true
	}
	for _, r := range templates {
		if strings.Count(r.Request, "{mac}") != 1 || strings.Count(r.File, "{mac}") != 1 {
			return errors.New("templates require exactly one {mac} in request and file")
		}
		for _, n := range []string{r.Request, r.File} {
			if _, e := store.Name(strings.ReplaceAll(n, "{mac}", "001122aabbcc")); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Server) resolve(name string) (string, error) {
	n, e := store.Name(name)
	if e != nil {
		return "", e
	}
	for _, r := range s.config.Remap {
		if n == r.From {
			return r.To, nil
		}
	}
	for _, r := range s.config.Templates {
		parts := strings.Split(r.Request, "{mac}")
		if !strings.HasPrefix(n, parts[0]) || !strings.HasSuffix(n, parts[1]) {
			continue
		}
		end := len(n) - len(parts[1])
		if end < len(parts[0]) {
			continue
		}
		mac := n[len(parts[0]):end]
		if len(mac) != 12 {
			continue
		}
		b, e := hex.DecodeString(mac)
		if e != nil || len(b) != 6 {
			continue
		}
		return store.Name(strings.ReplaceAll(r.File, "{mac}", strings.ToLower(mac)))
	}
	return n, nil
}
