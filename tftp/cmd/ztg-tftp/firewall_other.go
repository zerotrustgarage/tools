//go:build !windows

package main

import "errors"

func firewallCommand([]string) error {
	return errors.New("firewall rules are managed by the Windows MSI; configure your Linux firewall separately")
}
