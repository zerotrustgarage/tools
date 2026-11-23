package ad

import (
	"context"
	"errors"
	"strings"
)

// ErrInsufficientRights reports that AD refused a write because the signed-in
// account lacks the right (LDAP insufficientAccessRights, result 50).
var ErrInsufficientRights = errors.New("the signed-in account does not have the right to make this change")

// ErrAlreadyExists reports that an add or rename target already exists (LDAP
// entryAlreadyExists, result 68).
var ErrAlreadyExists = errors.New("an object with this name already exists")

// ErrConstraint reports a value AD rejected, for example a password that does
// not meet policy (LDAP constraintViolation 19 or unwillingToPerform 53).
var ErrConstraint = errors.New("Active Directory rejected the value")

// ErrInvalidCredentials reports that the domain controller refused the
// sign-in (LDAP invalidCredentials, result 49): a wrong password, or an account
// that is disabled, expired, locked or outside its logon hours. AD's own
// diagnostic text is included where available; the password never is.
var ErrInvalidCredentials = errors.New("the domain controller refused the sign-in")

// Credential is an explicit AD sign-in (design 6.5). User is a
// sAMAccountName (with Domain set) or a user principal name (Domain empty).
// Password is UTF-8 and is erased by Wipe; callers must Wipe when the session
// ends. A Credential is never written to disk, a log or a record.
type Credential struct {
	User     string
	Domain   string
	Password []byte
}

// Wipe overwrites the password bytes and clears the credential.
func (c *Credential) Wipe() {
	if c == nil {
		return
	}
	for i := range c.Password {
		c.Password[i] = 0
	}
	c.Password = nil
	c.User = ""
	c.Domain = ""
}

// Account returns DOMAIN\user or the UPN, for display and records. It never
// includes the password.
func (c *Credential) Account() string {
	if c == nil {
		return ""
	}
	if c.Domain == "" {
		return c.User
	}
	return c.Domain + `\` + c.User
}

// Attribute is one attribute with its values, used when creating objects.
type Attribute struct {
	Name   string
	Values []string
}

// ModOp is an LDAP modify operation.
type ModOp int

const (
	ModAdd ModOp = iota
	ModReplace
	ModDelete
)

func (o ModOp) String() string {
	switch o {
	case ModAdd:
		return "add"
	case ModReplace:
		return "replace"
	case ModDelete:
		return "delete"
	}
	return "unknown"
}

// Mod is one attribute modification. ModReplace with no values clears the
// attribute; ModDelete with no values removes every value.
type Mod struct {
	Op     ModOp
	Name   string
	Values []string
}

// Rights is what the signed-in account may write on one object, from AD's
// constructed allowedAttributesEffective and allowedChildClassesEffective
// attributes (research 5). Names are lower case. Extended rights such as Reset
// Password are not included; those are found by the attempt itself.
type Rights struct {
	Attributes   []string
	ChildClasses []string
}

// CanWrite reports whether attr appears in the effective attribute list.
func (r Rights) CanWrite(attr string) bool {
	attr = lowerASCII(attr)
	for _, a := range r.Attributes {
		if a == attr {
			return true
		}
	}
	return false
}

// CanCreate reports whether class appears in the effective child class list.
func (r Rights) CanCreate(class string) bool {
	class = lowerASCII(class)
	for _, c := range r.ChildClasses {
		if c == class {
			return true
		}
	}
	return false
}

// Writer is a Client opened with write support. Every write runs as the
// account in Config.Credential (or the current Windows identity when it is
// nil) over the same signed and sealed connection, so a plan reads, writes
// and verifies through one domain controller (runbook R119).
type Writer interface {
	Client
	// Add creates an object. attrs must include objectClass.
	Add(ctx context.Context, dn string, attrs []Attribute) error
	// Modify applies mods atomically to one object.
	Modify(ctx context.Context, dn string, mods []Mod) error
	// Delete removes a leaf object (to the Recycle Bin when it is enabled).
	Delete(ctx context.Context, dn string) error
	// Rename changes the RDN and, when newParent is not empty, moves the
	// object under newParent.
	Rename(ctx context.Context, dn, newRDN, newParent string) error
	// SetPassword replaces unicodePwd. password is UTF-8 and is not retained.
	SetPassword(ctx context.Context, dn string, password []byte) error
	// Undelete restores a deleted object from the AD Recycle Bin to newDN.
	Undelete(ctx context.Context, deletedDN, newDN string) error
	// Deleted searches the Deleted Objects container.
	Deleted(ctx context.Context, req SearchRequest) (SearchResult, error)
	// Rights reads the effective write rights of the signed-in account.
	Rights(ctx context.Context, dn string) (Rights, error)
	// DC names the domain controller this writer is bound to.
	DC() string
	// Identity names the bound account (DOMAIN\user or UPN).
	Identity() string
}

// ParentDN returns the parent of dn, or "" for a top-level DN. It honours
// escaped commas in RDN values.
func ParentDN(dn string) string {
	i := firstUnescapedComma(dn)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(dn[i+1:])
}

// RDN returns the first relative distinguished name of dn, for example
// "CN=Alex Smith".
func RDN(dn string) string {
	i := firstUnescapedComma(dn)
	if i < 0 {
		return strings.TrimSpace(dn)
	}
	return strings.TrimSpace(dn[:i])
}

// UnderDN reports whether dn equals base or lies below it (case-insensitive).
func UnderDN(dn, base string) bool {
	dn, base = lowerASCII(strings.TrimSpace(dn)), lowerASCII(strings.TrimSpace(base))
	if base == "" {
		return true
	}
	return dn == base || strings.HasSuffix(dn, ","+base)
}

// EscapeRDNValue escapes a value for use in an RDN (RFC 4514).
func EscapeRDNValue(v string) string {
	var b strings.Builder
	for i, r := range v {
		switch {
		case r == ',' || r == '+' || r == '"' || r == '\\' || r == '<' || r == '>' || r == ';' || r == '=':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '#' && i == 0:
			b.WriteString(`\#`)
		case r == ' ' && (i == 0 || i == len(v)-1):
			b.WriteString(`\ `)
		case r == 0:
			b.WriteString(`\00`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstUnescapedComma(dn string) int {
	escaped := false
	for i := 0; i < len(dn); i++ {
		switch {
		case escaped:
			escaped = false
		case dn[i] == '\\':
			escaped = true
		case dn[i] == ',':
			return i
		}
	}
	return -1
}

// UserAccountControl flags used by the planner (MS-ADTS 2.2.16).
const (
	UACAccountDisable       = 0x0002
	UACLockout              = 0x0010
	UACPasswordNotRequired  = 0x0020
	UACNormalAccount        = 0x0200
	UACServerTrustAccount   = 0x2000
	UACDontExpirePassword   = 0x10000
	UACPartialSecretsServer = 0x4000000
)
