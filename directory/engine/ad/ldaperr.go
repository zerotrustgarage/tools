package ad

import (
	"errors"
	"fmt"
	"strings"
)

// errNotSealed is returned when a password would travel over a connection
// that is neither sealed nor LDAPS (research 5, MS-ADTS 3.1.1.3.1.5.1).
var errNotSealed = errors.New("a password is written only over a sealed Negotiate connection or LDAPS")

// LDAP result codes (RFC 4511 section 4.1.9) and the WinLDAP client-side codes
// (winldap.h) that ldapError names.
var ldapResultNames = map[uint32]string{
	1:    "operationsError",
	2:    "protocolError",
	3:    "timeLimitExceeded",
	4:    "sizeLimitExceeded",
	8:    "strongerAuthRequired",
	10:   "referral",
	11:   "adminLimitExceeded",
	12:   "unavailableCriticalExtension",
	13:   "confidentialityRequired",
	16:   "noSuchAttribute",
	17:   "undefinedAttributeType",
	19:   "constraintViolation",
	20:   "attributeOrValueExists",
	21:   "invalidAttributeSyntax",
	32:   "noSuchObject",
	34:   "invalidDNSyntax",
	48:   "inappropriateAuthentication",
	49:   "invalidCredentials",
	50:   "insufficientAccessRights",
	51:   "busy",
	52:   "unavailable",
	53:   "unwillingToPerform",
	54:   "loopDetect",
	64:   "namingViolation",
	65:   "objectClassViolation",
	66:   "notAllowedOnNonLeaf",
	67:   "notAllowedOnRDN",
	68:   "entryAlreadyExists",
	69:   "objectClassModsProhibited",
	71:   "affectsMultipleDSAs",
	80:   "other",
	0x51: "server down",
	0x52: "local error",
	0x53: "encoding error",
	0x54: "decoding error",
	0x55: "timeout",
	0x56: "unknown authentication method",
	0x57: "filter error",
	0x58: "user cancelled",
	0x59: "parameter error",
	0x5a: "no memory",
	0x5b: "connect error",
	0x5c: "not supported",
	0x5d: "control not found",
	0x5e: "no results returned",
	0x5f: "more results to return",
	0x60: "client loop",
	0x61: "referral limit exceeded",
}

// ldapResultError maps an LDAP result code to the engine's errors so callers
// can use errors.Is, and keeps AD's diagnostic text (LDAP_OPT_SERVER_ERROR),
// which names the failing check, for example "0000052D: Constraint violation"
// for a password that fails policy. op names what was attempted. Neither op
// nor the diagnostic ever carries a password.
func ldapResultError(op string, code uint32, serverMessage string) error {
	detail := fmt.Sprintf("%s failed (LDAP %d %s)", op, code, ldapResultName(code))
	if msg := cleanServerMessage(serverMessage); msg != "" {
		detail += ": " + msg
	}
	var sentinel error
	switch code {
	case 32:
		sentinel = ErrNotFound
	case 49:
		sentinel = ErrInvalidCredentials
	case 50:
		sentinel = ErrInsufficientRights
	case 68:
		sentinel = ErrAlreadyExists
	case 19, 20, 21, 53, 64, 65, 66, 67:
		sentinel = ErrConstraint
	default:
		return errors.New(detail)
	}
	return fmt.Errorf("%w: %s", sentinel, detail)
}

func ldapResultName(code uint32) string {
	if name, ok := ldapResultNames[code]; ok {
		return name
	}
	return "unknown result"
}

// cleanServerMessage makes AD's diagnostic text safe to show and record:
// control characters (AD ends the text with a NUL or newline) are dropped and
// the length is bounded.
func cleanServerMessage(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		b.WriteRune(r)
		if b.Len() >= 512 {
			break
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
