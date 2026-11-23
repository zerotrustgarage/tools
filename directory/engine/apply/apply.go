// Package apply runs a reviewed plan: the checks it was reviewed under are
// repeated, each change is written as the signed-in account on the plan's DC,
// read back to verify it, and recorded before the next change starts (design
// principles 1 and 5). A failed step stops the plan.
package apply

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/plan"
	"github.com/zerotrustgarage/tools/directory/engine/record"
)

// Status of one step.
const (
	StatusDone   = "done"
	StatusFailed = "failed"
	StatusNotRun = "not-run"
)

// Options carry the confirmations and the people named in the record.
type Options struct {
	Log *record.Log
	// ReviewedHash is the plan hash the person reviewed; Apply refuses a plan
	// whose content differs.
	ReviewedHash string
	// TypedCount is the object count typed back for bulk plans.
	TypedCount int
	// ConfirmTier0 is the second confirmation for Tier 0 plans.
	ConfirmTier0 bool
	Requester    string
	Approver     string
	Method       string
	Device       string
	// Password generates temporary passwords; nil uses Generate.
	Password func() (string, error)
}

// Step is the outcome of one change.
type Step struct {
	Change plan.Change `json:"change"`
	Status string      `json:"status"`
	Error  string      `json:"error,omitempty"`
	// Password is the temporary password set by this step, shown once to the
	// technician and never recorded.
	Password string `json:"password,omitempty"`
}

// Result of a run.
type Result struct {
	PlanID string `json:"planId"`
	Hash   string `json:"hash"`
	Steps  []Step `json:"steps"`
	OK     bool   `json:"ok"`
}

// Run applies p through w.
func Run(ctx context.Context, w ad.Writer, p plan.Plan, o Options) (Result, error) {
	res := Result{PlanID: p.ID, Hash: p.Hash()}
	switch {
	case o.Log == nil:
		return res, errors.New("a change record is required before anything is written")
	case p.Blocked():
		return res, errors.New("the plan has blocking warnings and cannot run")
	case len(p.Changes) == 0:
		return res, errors.New("the plan has no changes")
	case o.ReviewedHash != "" && o.ReviewedHash != res.Hash:
		return res, errors.New("the plan changed after it was reviewed; build and review it again")
	case p.TypedCount && o.TypedCount != p.Objects:
		return res, fmt.Errorf("this plan changes %d objects; type that number to confirm", p.Objects)
	case p.Tier0 && !o.ConfirmTier0:
		return res, errors.New("this plan touches Tier 0 objects and needs the second confirmation")
	case !strings.EqualFold(w.DC(), p.DC):
		return res, fmt.Errorf("the plan was built on %s but the session is bound to %s; build it again", p.DC, w.DC())
	}
	gen := o.Password
	if gen == nil {
		gen = Generate
	}
	base := record.Entry{Kind: record.KindChange, Job: p.ID, PlanHash: res.Hash, Requester: firstNonEmpty(o.Requester, w.Identity()),
		Approver: o.Approver, Technician: w.Identity(), Actor: w.Identity(), DC: w.DC(), Method: o.Method, Device: o.Device, Ticket: p.Ticket}
	start := base
	start.Result, start.Note = "started", fmt.Sprintf("%s (%d changes, %d objects)", p.Title, len(p.Changes), p.Objects)
	if _, err := o.Log.Append(start); err != nil {
		return res, fmt.Errorf("the change record could not be written, so nothing was changed: %w", err)
	}
	failed := false
	for i, c := range p.Changes {
		step := Step{Change: c, Status: StatusNotRun}
		if failed {
			res.Steps = append(res.Steps, step)
			continue
		}
		pw, err := runChange(ctx, w, c, gen)
		e := base
		e.Step, e.Change, e.DN, e.Attribute, e.Before, e.After, e.Note = i+1, string(c.Kind), c.DN, c.Attribute, c.Before, c.After, c.Summary
		if c.Kind == plan.KindResetPassword || (c.Kind == plan.KindCreate && pw != "") {
			e.After = []string{"(temporary password set; not recorded)"}
		}
		if c.Kind == plan.KindMove || c.Kind == plan.KindRename || c.Kind == plan.KindUndelete || c.Kind == plan.KindAddMember || c.Kind == plan.KindRemoveMember {
			e.After = append(append([]string(nil), c.After...), c.Target)
		}
		if err != nil {
			step.Status, step.Error = StatusFailed, err.Error()
			e.Result, e.Error = StatusFailed, err.Error()
			failed = true
		} else {
			step.Status, step.Password = StatusDone, pw
			e.Result = StatusDone
		}
		if _, rerr := o.Log.Append(e); rerr != nil {
			res.Steps = append(res.Steps, step)
			for _, rest := range p.Changes[i+1:] {
				res.Steps = append(res.Steps, Step{Change: rest, Status: StatusNotRun})
			}
			return res, fmt.Errorf("step %d ran (%s) but its record could not be written; the remaining steps were stopped: %w", i+1, step.Status, rerr)
		}
		res.Steps = append(res.Steps, step)
	}
	end := base
	end.Result = "finished"
	if failed {
		end.Result = "stopped"
	}
	_, _ = o.Log.Append(end)
	res.OK = !failed
	return res, nil
}

func runChange(ctx context.Context, w ad.Writer, c plan.Change, gen func() (string, error)) (string, error) {
	switch c.Kind {
	case plan.KindSet:
		cur, err := w.Get(ctx, c.DN)
		if err != nil {
			return "", err
		}
		if !sameSet(cur.Values(c.Attribute), c.Before) {
			return "", fmt.Errorf("%s changed after the plan was reviewed (now %q); build the plan again", c.Attribute, cur.Values(c.Attribute))
		}
		mod := ad.Mod{Op: ad.ModReplace, Name: c.Attribute, Values: c.After}
		if len(c.After) == 0 {
			if len(c.Before) == 0 {
				return "", nil
			}
			mod = ad.Mod{Op: ad.ModDelete, Name: c.Attribute}
		}
		if err := w.Modify(ctx, c.DN, []ad.Mod{mod}); err != nil {
			return "", err
		}
		return "", verify(ctx, w, c.DN, func(o ad.Object) bool { return sameSet(o.Values(c.Attribute), c.After) }, c.Attribute+" did not read back as planned")
	case plan.KindAddMember, plan.KindRemoveMember:
		op := ad.ModAdd
		if c.Kind == plan.KindRemoveMember {
			op = ad.ModDelete
		}
		if err := w.Modify(ctx, c.DN, []ad.Mod{{Op: op, Name: "member", Values: []string{c.Target}}}); err != nil {
			return "", err
		}
		want := c.Kind == plan.KindAddMember
		return "", verify(ctx, w, c.Target, func(o ad.Object) bool { return containsFold(o.Values("memberof"), c.DN) == want }, "group membership did not read back as planned")
	case plan.KindMove:
		cur, err := w.Get(ctx, c.DN)
		if err != nil {
			return "", err
		}
		if err := w.Rename(ctx, cur.DN, ad.RDN(cur.DN), c.Target); err != nil {
			return "", err
		}
		return "", verify(ctx, w, ad.RDN(cur.DN)+","+c.Target, nil, "the object was not found in its new OU")
	case plan.KindRename:
		if err := w.Rename(ctx, c.DN, c.Target, ""); err != nil {
			return "", err
		}
		return "", verify(ctx, w, c.Target+","+ad.ParentDN(c.DN), nil, "the object was not found under its new name")
	case plan.KindEnable, plan.KindDisable:
		cur, err := w.Get(ctx, c.DN)
		if err != nil {
			return "", err
		}
		uac, err := strconv.ParseInt(cur.First("useraccountcontrol"), 10, 64)
		if err != nil {
			return "", errors.New("userAccountControl could not be read")
		}
		next := uac | ad.UACAccountDisable
		if c.Kind == plan.KindEnable {
			next = uac &^ ad.UACAccountDisable
		}
		if next != uac {
			if err := w.Modify(ctx, c.DN, []ad.Mod{{Op: ad.ModReplace, Name: "userAccountControl", Values: []string{strconv.FormatInt(next, 10)}}}); err != nil {
				return "", err
			}
		}
		return "", verify(ctx, w, c.DN, func(o ad.Object) bool { return o.First("useraccountcontrol") == strconv.FormatInt(next, 10) }, "the account state did not read back as planned")
	case plan.KindUnlock:
		if err := w.Modify(ctx, c.DN, []ad.Mod{{Op: ad.ModReplace, Name: "lockoutTime", Values: []string{"0"}}}); err != nil {
			return "", err
		}
		return "", verify(ctx, w, c.DN, func(o ad.Object) bool { v := o.First("lockouttime"); return v == "" || v == "0" }, "the account still reads as locked")
	case plan.KindResetPassword:
		return setPassword(ctx, w, c.DN, c.MustChange, gen)
	case plan.KindDelete:
		if err := w.Delete(ctx, c.DN); err != nil {
			return "", err
		}
		if _, err := w.Get(ctx, c.DN); !errors.Is(err, ad.ErrNotFound) {
			return "", errors.New("the object still exists after the delete")
		}
		return "", nil
	case plan.KindUndelete:
		if err := w.Undelete(ctx, c.DN, c.Target); err != nil {
			return "", err
		}
		return "", verify(ctx, w, c.Target, nil, "the restored object was not found")
	case plan.KindCreate:
		attrs := append([]ad.Attribute(nil), c.Attributes...)
		isUser := c.Class == "user" || c.Class == "inetorgperson"
		if isUser {
			attrs = append(attrs, ad.Attribute{Name: "userAccountControl", Values: []string{strconv.Itoa(ad.UACNormalAccount | ad.UACAccountDisable)}})
		}
		if err := w.Add(ctx, c.DN, attrs); err != nil {
			return "", err
		}
		if !isUser {
			return "", verify(ctx, w, c.DN, nil, "the new object was not found")
		}
		pw, err := setPassword(ctx, w, c.DN, c.MustChange, gen)
		if err != nil {
			return "", fmt.Errorf("the account was created disabled, but its password could not be set: %w", err)
		}
		if c.Enable {
			if err := w.Modify(ctx, c.DN, []ad.Mod{{Op: ad.ModReplace, Name: "userAccountControl", Values: []string{strconv.Itoa(ad.UACNormalAccount)}}}); err != nil {
				return pw, fmt.Errorf("the account was created with a password but could not be enabled: %w", err)
			}
		}
		return pw, verify(ctx, w, c.DN, nil, "the new account was not found")
	}
	return "", fmt.Errorf("unknown change kind %q", c.Kind)
}

func setPassword(ctx context.Context, w ad.Writer, dn string, mustChange bool, gen func() (string, error)) (string, error) {
	pw, err := gen()
	if err != nil {
		return "", err
	}
	b := []byte(pw)
	err = w.SetPassword(ctx, dn, b)
	for i := range b {
		b[i] = 0
	}
	if err != nil {
		return "", err
	}
	if mustChange {
		if err := w.Modify(ctx, dn, []ad.Mod{{Op: ad.ModReplace, Name: "pwdLastSet", Values: []string{"0"}}}); err != nil {
			return pw, fmt.Errorf("the password was set but change at next logon could not be set: %w", err)
		}
	}
	return pw, nil
}

func verify(ctx context.Context, w ad.Writer, dn string, ok func(ad.Object) bool, msg string) error {
	o, err := w.Get(ctx, dn)
	if err != nil {
		return fmt.Errorf("verification read failed: %w", err)
	}
	if ok != nil && !ok(o) {
		return errors.New(msg)
	}
	return nil
}

// Generate returns a temporary password of four groups of four characters
// (for example "Kx7m-Pq2r-Ft9w-Lz3c"), with upper and lower case letters and
// digits, from crypto/rand. Ambiguous characters are left out.
func Generate() (string, error) {
	const upper, lower, digit = "ABCDEFGHJKLMNPQRSTUVWXYZ", "abcdefghijkmnopqrstuvwxyz", "23456789"
	all := upper + lower + digit
	for {
		var b strings.Builder
		var hasU, hasL, hasD bool
		for i := 0; i < 16; i++ {
			if i > 0 && i%4 == 0 {
				b.WriteByte('-')
			}
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(all))))
			if err != nil {
				return "", err
			}
			ch := all[n.Int64()]
			switch {
			case strings.IndexByte(upper, ch) >= 0:
				hasU = true
			case strings.IndexByte(lower, ch) >= 0:
				hasL = true
			default:
				hasD = true
			}
			b.WriteByte(ch)
		}
		if hasU && hasL && hasD {
			return b.String(), nil
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !containsFold(b, x) {
			return false
		}
	}
	return true
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
