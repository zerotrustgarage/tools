// Package mirror is the Microsoft 365 mirror pane's engine (design 7): it finds
// the cloud user that mirrors an AD user, by source anchor and then by SID,
// summarizes it and compares each synced attribute. Directory and tenant
// values are untrusted; the console inserts them as text only.
package mirror

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/m365"
)

// How an AD object was matched to its cloud object.
const (
	MatchAnchor   = "linked by source anchor"
	MatchSID      = "linked by SID"
	MatchPossible = "possible match"
	MatchNone     = "not in Microsoft 365"
	// MatchUnsupported is returned for object kinds the mirror does not
	// cover yet (groups, contacts and computers).
	MatchUnsupported = "not compared"
)

// Comparison statuses of one attribute row.
const (
	StatusMatch = "match"
	// StatusWaiting: the values differ and AD changed after the cloud
	// user's last sync, so the next cycle should carry the change.
	StatusWaiting = "waiting for sync"
	// StatusOutOfStep: the values differ although the last sync came after
	// the AD change; it needs attention.
	StatusOutOfStep = "out of step"
	// StatusNotCompared: the AD side could not be read (for example the
	// manager's UPN without a resolver).
	StatusNotCompared = "not compared"
)

// Anchor is the AD source anchor and the immutable ID it gives.
type Anchor struct {
	Attribute   string `json:"attribute,omitempty"` // mS-DS-ConsistencyGuid or objectGUID
	GUID        string `json:"guid,omitempty"`
	ImmutableID string `json:"immutableId,omitempty"`
	SID         string `json:"sid,omitempty"`
	Note        string `json:"note,omitempty"`
}

// Summary is the cloud user as the pane shows it. Times are RFC 3339 or empty.
type Summary struct {
	ID                 string                   `json:"id"`
	UserPrincipalName  string                   `json:"userPrincipalName"`
	DisplayName        string                   `json:"displayName"`
	Mail               string                   `json:"mail,omitempty"`
	SignInEnabled      *bool                    `json:"signInEnabled,omitempty"`
	Created            string                   `json:"created,omitempty"`
	LastSignIn         string                   `json:"lastSignIn,omitempty"` // last successful cloud sign-in (Entra ID P1)
	LastSync           string                   `json:"lastSync,omitempty"`
	SyncEnabled        *bool                    `json:"syncEnabled,omitempty"`
	Source             string                   `json:"source"` // source of authority: "Active Directory" or "Microsoft 365"
	ImmutableID        string                   `json:"immutableId,omitempty"`
	SID                string                   `json:"sid,omitempty"`
	ProvisioningErrors []m365.ProvisioningError `json:"provisioningErrors,omitempty"`
}

// Candidate is a cloud user that shares a name with the AD object but is not
// linked to it.
type Candidate struct {
	User    Summary  `json:"user"`
	Reasons []string `json:"reasons"` // why it looks like the same person, and why it is not linked
}

// GroupSet is the cloud groups of one kind.
type GroupSet struct {
	Kind   string       `json:"kind"` // synced, cloud, microsoft365, role, other
	Label  string       `json:"label"`
	Groups []m365.Group `json:"groups"`
}

// Row is one attribute in the comparison table.
type Row struct {
	Attribute string   `json:"attribute"` // AD attribute
	Property  string   `json:"property"`  // Graph property
	Label     string   `json:"label"`
	AD        []string `json:"ad"`
	Cloud     []string `json:"cloud"`
	Status    string   `json:"status"`
	Note      string   `json:"note,omitempty"`
}

// View is the mirror pane for one AD object.
type View struct {
	DN         string      `json:"dn"`
	Kind       string      `json:"kind"`
	Account    string      `json:"account"` // the signed-in cloud account
	Match      string      `json:"match"`
	Reason     string      `json:"reason"`
	Hint       string      `json:"hint,omitempty"` // "Out of sync scope? ..." when no cloud user was found
	Anchor     Anchor      `json:"anchor"`
	User       *Summary    `json:"user,omitempty"`
	Candidates []Candidate `json:"candidates,omitempty"`
	// Licenses are the cloud user's licenses; ViaGroup marks group-based ones.
	Licenses    []m365.License `json:"licenses,omitempty"`
	Groups      []GroupSet     `json:"groups,omitempty"`
	GroupsNote  string         `json:"groupsNote,omitempty"`
	Devices     []m365.Device  `json:"devices,omitempty"`
	DevicesNote string         `json:"devicesNote,omitempty"`
	Methods     []string       `json:"methods,omitempty"` // registered sign-in method kinds, password excluded
	MethodsNote string         `json:"methodsNote,omitempty"`
	Rows        []Row          `json:"rows,omitempty"`
	Differences int            `json:"differences"` // rows waiting for sync or out of step
	Waiting     int            `json:"waiting"`     // rows waiting for sync
	ADChanged   string         `json:"adChanged,omitempty"`
	CheckedAt   string         `json:"checkedAt"`
}

// Option adjusts Mirror.
type Option func(*options)

type options struct {
	manager func(ctx context.Context, dn string) (string, error)
	now     func() time.Time
}

// WithManagerResolver supplies the UPN of the manager DN, so the manager row
// can be compared with the cloud managerUpn. Without it the row is "not
// compared".
func WithManagerResolver(f func(ctx context.Context, dn string) (string, error)) Option {
	return func(o *options) { o.manager = f }
}

// WithClock sets the time CheckedAt reports (tests).
func WithClock(now func() time.Time) Option { return func(o *options) { o.now = now } }

// Mirror finds o's cloud user in t and compares them. Matching follows design
// 7: onPremisesImmutableId against the Base64 of the source anchor
// (mS-DS-ConsistencyGuid, then objectGUID), then onPremisesSecurityIdentifier
// against objectSid; a UPN or primary SMTP match without either is only a
// possible match. ErrForbidden on the lookups fails the view; on groups,
// devices or sign-in methods it becomes that section's note.
func Mirror(ctx context.Context, t m365.Tenant, o ad.Object, opts ...Option) (View, error) {
	op := options{now: func() time.Time { return time.Now().UTC() }}
	for _, f := range opts {
		f(&op)
	}
	v := View{DN: o.DN, Kind: kindOf(o), Account: t.Account(), CheckedAt: op.now().UTC().Format(time.RFC3339)}
	if ch := ad.GeneralizedTime(o.First("whenchanged")); !ch.IsZero() {
		v.ADChanged = ch.UTC().Format(time.RFC3339)
	}
	if v.Kind != "user" {
		v.Match = MatchUnsupported
		v.Reason = "The Microsoft 365 mirror covers users in this version; groups and contacts follow."
		return v, nil
	}
	anchors := sourceAnchors(o, &v.Anchor)
	v.Anchor.SID = o.First("objectsid")

	var u *m365.User
	for _, a := range anchors {
		found, err := t.UserByImmutableID(ctx, a.id)
		if err != nil {
			return View{}, lookupErr(err)
		}
		if found != nil {
			u = found
			v.Match = MatchAnchor
			v.Anchor.Attribute, v.Anchor.GUID, v.Anchor.ImmutableID = a.attr, a.guid, a.id
			v.Reason = "onPremisesImmutableId equals the Base64 of " + a.attr + "."
			break
		}
	}
	if u == nil && v.Anchor.SID != "" {
		found, err := t.UserBySID(ctx, v.Anchor.SID)
		if err != nil {
			return View{}, lookupErr(err)
		}
		if found != nil {
			u = found
			v.Match = MatchSID
			v.Reason = "onPremisesSecurityIdentifier equals objectSid, but onPremisesImmutableId (" + quoteOr(found.OnPremisesImmutableID, "empty") +
				") is not the Base64 of this object's source anchor: the tenant may use another source anchor, or the anchor changed."
		}
	}
	if u == nil {
		cands, err := candidates(ctx, t, o)
		if err != nil {
			return View{}, lookupErr(err)
		}
		if len(cands) > 0 {
			v.Match, v.Candidates = MatchPossible, cands
			v.Reason = "A cloud user has the same sign-in name or email address but neither the source anchor nor the SID links it to this AD object, so it is shown as a possible match, not linked."
			return v, nil
		}
		v.Match = MatchNone
		v.Reason = "No cloud user has this object's source anchor, SID, UPN or primary email address."
		v.Hint = scopeHint(ctx, t, o)
		return v, nil
	}

	s := summarize(*u)
	v.User = &s
	v.Licenses = append([]m365.License(nil), u.Licenses...)
	if err := v.optional(ctx, t, u.ID); err != nil {
		return View{}, err
	}
	var managerUPN *string
	if dn := o.First("manager"); dn == "" {
		empty := ""
		managerUPN = &empty
	} else if op.manager != nil {
		if upn, err := op.manager(ctx, dn); err == nil {
			managerUPN = &upn
		} else if ctx.Err() != nil {
			return View{}, ctx.Err()
		}
	}
	v.Rows = compare(o, *u, managerUPN)
	for _, r := range v.Rows {
		switch r.Status {
		case StatusWaiting:
			v.Waiting++
			v.Differences++
		case StatusOutOfStep:
			v.Differences++
		}
	}
	return v, nil
}

func lookupErr(err error) error {
	if errors.Is(err, m365.ErrForbidden) {
		return fmt.Errorf("finding the cloud user: %w (the mirror needs User.Read.All)", err)
	}
	return err
}

func kindOf(o ad.Object) string {
	cls := map[string]bool{}
	for _, c := range o.Values("objectclass") {
		cls[strings.ToLower(c)] = true
	}
	switch {
	case cls["computer"]:
		return "computer"
	case cls["user"] || cls["inetorgperson"]:
		return "user"
	case cls["group"]:
		return "group"
	case cls["contact"]:
		return "contact"
	}
	return "other"
}

type anchor struct{ attr, guid, id string }

// sourceAnchors lists the immutable IDs to try: mS-DS-ConsistencyGuid first,
// then objectGUID when it differs.
func sourceAnchors(o ad.Object, a *Anchor) []anchor {
	var out []anchor
	for _, c := range []struct{ attr, key string }{{"mS-DS-ConsistencyGuid", "ms-ds-consistencyguid"}, {"objectGUID", "objectguid"}} {
		g := o.First(c.key)
		if g == "" {
			continue
		}
		id, err := ad.ImmutableID(g)
		if err != nil {
			a.Note = strings.TrimSpace(a.Note + " " + c.attr + " is not a 16-byte GUID, so it cannot be the source anchor.")
			continue
		}
		if len(out) > 0 && out[0].id == id {
			continue
		}
		out = append(out, anchor{c.attr, g, id})
	}
	if len(out) > 0 {
		a.Attribute, a.GUID, a.ImmutableID = out[0].attr, out[0].guid, out[0].id
	} else {
		a.Note = strings.TrimSpace(a.Note + " No source anchor could be read from AD.")
	}
	return out
}

func primarySMTP(o ad.Object) string {
	for _, p := range o.Values("proxyaddresses") {
		if strings.HasPrefix(p, "SMTP:") {
			return p[5:]
		}
	}
	return o.First("mail")
}

func candidates(ctx context.Context, t m365.Tenant, o ad.Object) ([]Candidate, error) {
	var out []Candidate
	index := map[string]int{}
	look := func(value, why string) error {
		if value == "" {
			return nil
		}
		users, err := t.UsersByName(ctx, value)
		if err != nil {
			return err
		}
		for _, u := range users {
			reason := ""
			switch {
			case strings.EqualFold(u.UserPrincipalName, value) && strings.EqualFold(u.Mail, value):
				reason = "Its userPrincipalName and mail equal the AD " + why + " (" + value + ")."
			case strings.EqualFold(u.UserPrincipalName, value):
				reason = "Its userPrincipalName equals the AD " + why + " (" + value + ")."
			default:
				reason = "Its mail equals the AD " + why + " (" + value + ")."
			}
			if i, ok := index[u.ID]; ok {
				if !containsString(out[i].Reasons, reason) {
					out[i].Reasons = append([]string{reason}, out[i].Reasons...)
				}
				continue
			}
			c := Candidate{User: summarize(u), Reasons: []string{reason}}
			if u.OnPremisesImmutableID == "" {
				c.Reasons = append(c.Reasons, "It is a cloud-only account (no onPremisesImmutableId), so it is a different object. Syncing this AD user would join them only by a soft match, which should stay blocked except during a planned match.")
			} else {
				c.Reasons = append(c.Reasons, "It is linked to another AD object (onPremisesImmutableId "+u.OnPremisesImmutableID+"), not this one.")
			}
			index[u.ID] = len(out)
			out = append(out, c)
		}
		return nil
	}
	upn := o.First("userprincipalname")
	if err := look(upn, "userPrincipalName"); err != nil {
		return nil, err
	}
	if smtp := primarySMTP(o); !strings.EqualFold(smtp, upn) {
		if err := look(smtp, "primary SMTP address"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// scopeHint suggests why a user is not in Microsoft 365.
func scopeHint(ctx context.Context, t m365.Tenant, o ad.Object) string {
	if o.First("admincount") == "1" {
		return "Out of sync scope? This is a protected account (adminCount = 1); administrative accounts are normally kept out of the sync scope on purpose."
	}
	if org, err := t.Organization(ctx); err == nil && org.OnPremisesLastSyncDateTime != nil {
		created := ad.GeneralizedTime(o.First("whencreated"))
		if !created.IsZero() && created.After(*org.OnPremisesLastSyncDateTime) {
			return "Created in AD after the tenant's last sync cycle (" + org.OnPremisesLastSyncDateTime.UTC().Format(time.RFC3339) + "): it should appear after the next cycle, or use Sync now."
		}
		if !org.OnPremisesSyncEnabled {
			return "Directory synchronization is not enabled for this tenant."
		}
	}
	return "Out of sync scope? The sync configuration may filter this object's OU, group or attributes; check the scope on the sync server."
}

func summarize(u m365.User) Summary {
	s := Summary{ID: u.ID, UserPrincipalName: u.UserPrincipalName, DisplayName: u.DisplayName, Mail: u.Mail, SignInEnabled: u.AccountEnabled,
		Created: stamp(u.CreatedDateTime), LastSignIn: stamp(u.LastSuccessfulSignIn), LastSync: stamp(u.OnPremisesLastSyncDateTime),
		SyncEnabled: u.OnPremisesSyncEnabled, ImmutableID: u.OnPremisesImmutableID, SID: u.OnPremisesSecurityIdentifier,
		ProvisioningErrors: append([]m365.ProvisioningError(nil), u.ProvisioningErrors...), Source: "Microsoft 365"}
	if u.OnPremisesSyncEnabled != nil && *u.OnPremisesSyncEnabled {
		s.Source = "Active Directory"
	}
	return s
}

func stamp(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// optional reads groups, devices and sign-in methods; a refusal or failure of
// one becomes its note. Only a cancelled context fails the view.
func (v *View) optional(ctx context.Context, t m365.Tenant, id string) error {
	note := func(err error, what, perm string) string {
		if errors.Is(err, m365.ErrForbidden) {
			return "Microsoft 365 refused " + what + " for your account: it needs the " + perm + " delegated permission and an Entra role that allows reading it."
		}
		return what + " could not be read: " + err.Error()
	}
	gs, err := t.UserGroups(ctx, id)
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if err != nil {
		v.GroupsNote = note(err, "the cloud group memberships", "GroupMember.Read.All")
	} else {
		v.Groups = groupSets(gs)
	}
	ds, err := t.UserDevices(ctx, id)
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if err != nil {
		v.DevicesNote = note(err, "the registered devices", "Device.Read.All")
	} else {
		v.Devices = ds
	}
	ms, err := t.AuthMethods(ctx, id)
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if err != nil {
		v.MethodsNote = note(err, "the sign-in methods", "UserAuthenticationMethod.Read.All")
	} else {
		v.Methods = ms
	}
	return nil
}

var groupKinds = []struct{ kind, label string }{
	{"synced", "Synced from Active Directory"},
	{"cloud", "Cloud security groups"},
	{"microsoft365", "Microsoft 365 groups"},
	{"role", "Role-assignable groups"},
	{"other", "Other groups"},
}

func groupSets(gs []m365.Group) []GroupSet {
	by := map[string][]m365.Group{}
	for _, g := range gs {
		k := g.Kind
		switch k {
		case "synced", "cloud", "microsoft365", "role":
		default:
			k = "other"
		}
		by[k] = append(by[k], g)
	}
	var out []GroupSet
	for _, k := range groupKinds {
		if len(by[k.kind]) == 0 {
			continue
		}
		list := by[k.kind]
		sort.Slice(list, func(i, j int) bool {
			return strings.ToLower(list[i].DisplayName) < strings.ToLower(list[j].DisplayName)
		})
		out = append(out, GroupSet{Kind: k.kind, Label: k.label, Groups: list})
	}
	return out
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func quoteOr(v, empty string) string {
	if v == "" {
		return empty
	}
	return v
}

func enabledText(o ad.Object) []string {
	n, err := strconv.ParseInt(o.First("useraccountcontrol"), 10, 64)
	if err != nil {
		return nil
	}
	return []string{strconv.FormatBool(n&ad.UACAccountDisable == 0)}
}
