package session

import (
	"errors"
	"github.com/zerotrustgarage/tools/tftp/internal/importer"
	"github.com/zerotrustgarage/tools/tftp/internal/server"
)

func applySources(c *server.Config, manifest, scanner string) error {
	c.Manifest = manifest
	if scanner != "" {
		if len(c.Allow) > 0 {
			return errors.New("choose an explicit allowlist or Scanner import")
		}
		allow, e := importer.Scanner(scanner)
		if e != nil {
			return e
		}
		c.Allow = allow
	}
	return nil
}
