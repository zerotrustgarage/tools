package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/directory/engine/demo"
)

func runWithInput(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	old := stdin
	stdin = strings.NewReader(input)
	defer func() { stdin = old }()
	var out, errOut bytes.Buffer
	err := run(context.Background(), args, &out, &errOut, nil, nil)
	return out.String() + errOut.String(), err
}

func TestDemoSetAppliesAfterConfirmation(t *testing.T) {
	dir := t.TempDir()
	bea := "CN=Bea Okafor,OU=Finance,OU=Staff,OU=Contoso Labs," + demo.Base
	out, err := runWithInput(t, "Demo-Password-1\nno\n", "set", "--demo", "--data", dir, bea, "title", "Controller")
	if err == nil || !strings.Contains(out, "Set title on Bea Okafor") {
		t.Fatalf("unconfirmed plan should stop: %v\n%s", err, out)
	}
	out, err = runWithInput(t, "Demo-Password-1\nyes\n", "set", "--demo", "--data", dir, "--ticket", "T-1", bea, "title", "Controller")
	if err != nil || !strings.Contains(out, "[done] Set title on Bea Okafor") || !strings.Contains(out, "without multifactor") {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	out, err = runWithInput(t, "", "records", "--demo", "--data", dir)
	if err != nil || !strings.Contains(out, "sign-in") || !strings.Contains(out, bea) {
		t.Fatalf("records: %v\n%s", err, out)
	}
	if out, err = runWithInput(t, "", "verify-records", "--demo", "--data", dir); err != nil || !strings.Contains(out, "intact") {
		t.Fatalf("verify: %v %s", err, out)
	}
}

func TestDemoResetPasswordShowsOnceAndTier0Refused(t *testing.T) {
	dir := t.TempDir()
	caleb := "CN=Caleb Nakamura,OU=Finance,OU=Staff,OU=Contoso Labs," + demo.Base
	out, err := runWithInput(t, "Demo-Password-1\nyes\n", "reset-password", "--demo", "--data", dir, caleb)
	if err != nil || !strings.Contains(out, "Temporary password (shown once, not recorded)") {
		t.Fatalf("reset: %v\n%s", err, out)
	}
	admin := "CN=Admin Riley Chen,OU=Admins,OU=Contoso Labs," + demo.Base
	out, err = runWithInput(t, "Demo-Password-1\n", "disable", "--demo", "--data", dir, admin)
	if err == nil || !strings.Contains(out, "BLOCKED") {
		t.Fatalf("Tier 0 change outside rights should be blocked: %v\n%s", err, out)
	}
}

func TestDemoNewFromTemplate(t *testing.T) {
	dir := t.TempDir()
	out, err := runWithInput(t, "Demo-Password-1\nyes\n", "new", "--demo", "--data", dir, "--template", "Engineering staff",
		"--field", "givenName=Noor", "--field", "sn=Haddad", "--field", "title=Engineer", "--field", "employeeID=E4001")
	if err != nil || !strings.Contains(out, "Create user Noor Haddad") || !strings.Contains(out, "Add Noor Haddad to VPN Users") {
		t.Fatalf("new: %v\n%s", err, out)
	}
	if _, err := runWithInput(t, "", "new", "--demo", "--data", dir); err == nil {
		t.Fatal("new without --template accepted")
	}
}

func TestLiveChangeRequiresSettings(t *testing.T) {
	_, err := runWithInput(t, "", "unlock", "--data", t.TempDir(), "--user", "x", "CN=A")
	if err == nil || !strings.Contains(err.Error(), "setup") {
		t.Fatalf("expected a setup hint: %v", err)
	}
}
