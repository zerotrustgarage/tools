//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Used by deferred, elevated MSI actions. No shell interprets program paths.
func firewallCommand(args []string) error {
	if len(args) != 1 || (args[0] != "install" && args[0] != "remove") {
		return errors.New("firewall requires install or remove")
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	dir := filepath.Dir(exe)
	netsh := filepath.Join(os.Getenv("SystemRoot"), "System32", "netsh.exe")
	for _, app := range []string{"ZTGTFTP.exe", "ztg-tftp.exe"} {
		name := "ZTG TFTP " + app + " {2082e148-404e-4a7c-8477-f7472143b644}"
		// Deletion is idempotent: netsh reports failure when the rule is already absent.
		_ = exec.Command(netsh, "advfirewall", "firewall", "delete", "rule", "name="+name).Run()
		if args[0] == "install" {
			out, e := exec.Command(netsh, "advfirewall", "firewall", "add", "rule", "name="+name, "dir=in", "action=allow", "enable=yes", "profile=domain,private", "protocol=UDP", "program="+filepath.Join(dir, app)).CombinedOutput()
			if e != nil {
				return fmt.Errorf("firewall rule: %w: %s", e, out)
			}
		}
	}
	return nil
}
