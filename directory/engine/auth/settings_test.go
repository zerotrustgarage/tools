package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validDuo() Settings {
	s := DefaultSettings()
	s.Domain = "example.com"
	s.BaseDN = "DC=example,DC=com"
	s.SecondFactor = SecondFactorDuo
	s.RADIUS.Server = "10.0.0.5:1812"
	s.RADIUS.SecretFile = `C:\ProgramData\ZTG Directory\radius.secret`
	return s
}

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	if !s.KerberosOnly || s.OperatorsGroup != "ZTG Directory Operators" || s.ApproversGroup != "ZTG Directory Approvers" ||
		s.IdleLockMinutes != 15 || s.MaxSessionHours != 8 || s.SecondFactor != "" || s.AllowPasswordOnly {
		t.Fatalf("unexpected defaults: %+v", s)
	}
	if s.RADIUS.UsernameFormat != UsernameSAM || s.RADIUS.TimeoutSeconds < 60 || s.RADIUS.NASIdentifier == "" {
		t.Fatalf("unexpected RADIUS defaults: %+v", s.RADIUS)
	}
	if s.IdleLock() != 15*time.Minute || s.MaxSession() != 8*time.Hour {
		t.Fatal("durations")
	}
	s.Domain = "example.com"
	if err := s.Validate(); !errors.Is(err, ErrPasswordOnlyNotAllowed) {
		t.Fatalf("defaults without a second factor: got %v", err)
	}
	s.AllowPasswordOnly = true
	if err := s.Validate(); err != nil {
		t.Fatalf("password only allowed: %v", err)
	}
	if s.Method() != MethodPassword || validDuo().Method() != MethodPasswordDuo {
		t.Fatal("method names")
	}
}

func TestValidate(t *testing.T) {
	if err := validDuo().Validate(); err != nil {
		t.Fatalf("valid settings: %v", err)
	}
	cases := map[string]func(*Settings){
		"no domain or server":     func(s *Settings) { s.Domain, s.Server = "", "" },
		"unknown second factor":   func(s *Settings) { s.SecondFactor = "sms" },
		"password only with duo":  func(s *Settings) { s.AllowPasswordOnly = true },
		"radius server no port":   func(s *Settings) { s.RADIUS.Server = "10.0.0.5" },
		"radius server bad port":  func(s *Settings) { s.RADIUS.Server = "10.0.0.5:70000" },
		"no secret file":          func(s *Settings) { s.RADIUS.SecretFile = "" },
		"timeout zero":            func(s *Settings) { s.RADIUS.TimeoutSeconds = 0 },
		"retries negative":        func(s *Settings) { s.RADIUS.Retries = -1 },
		"no NAS identifier":       func(s *Settings) { s.RADIUS.NASIdentifier = "" },
		"username format":         func(s *Settings) { s.RADIUS.UsernameFormat = "email" },
		"empty operators group":   func(s *Settings) { s.OperatorsGroup = " " },
		"same groups":             func(s *Settings) { s.ApproversGroup = "ztg directory operators" },
		"idle lock zero":          func(s *Settings) { s.IdleLockMinutes = 0 },
		"idle lock past max":      func(s *Settings) { s.IdleLockMinutes = 8 * 60 },
		"max session zero":        func(s *Settings) { s.MaxSessionHours = 0 },
		"max session too long":    func(s *Settings) { s.MaxSessionHours = 25 },
		"control char in base DN": func(s *Settings) { s.BaseDN = "DC=example\n,DC=com" },
		"notice too long":         func(s *Settings) { s.Notice = strings.Repeat("x", 17*1024) },
	}
	for name, mutate := range cases {
		s := validDuo()
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func writeSettings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "signin.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSettingsKeepsDefaults(t *testing.T) {
	path := writeSettings(t, `{"domain":"example.com","baseDN":"DC=example,DC=com","secondFactor":"radius",
		"radius":{"server":"nps.example.com:1812","secretFile":"C:\\secret.bin","usernameFormat":"upn"},
		"notice":"Authorized use only."}`)
	s, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.KerberosOnly || s.IdleLockMinutes != 15 || s.MaxSessionHours != 8 || s.RADIUS.TimeoutSeconds != 60 ||
		s.RADIUS.Retries != 1 || s.RADIUS.NASIdentifier != "ZTG Directory" || s.OperatorsGroup != "ZTG Directory Operators" {
		t.Fatalf("defaults lost: %+v", s)
	}
	if s.RADIUS.UsernameFormat != UsernameUPN || s.Notice != "Authorized use only." || s.Method() != MethodPasswordRADIUS {
		t.Fatalf("values not read: %+v", s)
	}

	path = writeSettings(t, `{"domain":"example.com","kerberosOnly":false,"allowPasswordOnly":true}`)
	if s, err = LoadSettings(path); err != nil || s.KerberosOnly {
		t.Fatalf("explicit kerberosOnly false: %+v %v", s, err)
	}
}

func TestLoadSettingsRejects(t *testing.T) {
	cases := map[string]string{
		"unknown field":         `{"domain":"example.com","allowPasswordOnly":true,"password":"x"}`,
		"unknown nested field":  `{"domain":"example.com","secondFactor":"duo","radius":{"server":"a:1812","secretFile":"f","secret":"x"}}`,
		"code-only field":       `{"domain":"example.com","allowPasswordOnly":true,"Now":null}`,
		"trailing data":         `{"domain":"example.com","allowPasswordOnly":true} {}`,
		"not JSON":              `domain=example.com`,
		"password only refused": `{"domain":"example.com"}`,
	}
	for name, body := range cases {
		if _, err := LoadSettings(writeSettings(t, body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := LoadSettings(writeSettings(t, `{"domain":"example.com"}`)); !errors.Is(err, ErrPasswordOnlyNotAllowed) {
		t.Errorf("password-only message: %v", err)
	}
	if _, err := LoadSettings(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file: expected an error")
	}
}

func TestOpenSecondFactorNone(t *testing.T) {
	s := DefaultSettings()
	s.Domain, s.AllowPasswordOnly = "example.com", true
	sf, err := OpenSecondFactor(s)
	if err != nil || sf != nil {
		t.Fatalf("got %v, %v", sf, err)
	}
}
