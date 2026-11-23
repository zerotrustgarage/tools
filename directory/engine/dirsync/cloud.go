package dirsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// GraphDoer sends one Microsoft Graph v1.0 request. path is relative to the
// version root (for example "/servicePrincipals"), with identifiers escaped and
// any query already encoded; body, when not nil, is sent as JSON and out, when
// not nil, receives the JSON answer. engine/m365's Graph client satisfies it.
type GraphDoer interface {
	Do(ctx context.Context, method, path string, body any, out any) error
}

// CloudSyncTemplateID is the application template of the Cloud Sync
// (AD2AAD) service principal, from "How to programmatically configure cloud
// sync using MS Graph API":
// https://learn.microsoft.com/en-us/entra/identity/hybrid/cloud-sync/how-to-inbound-synch-ms-graph
const CloudSyncTemplateID = "1a4721b3-e57f-4451-ae87-ef078703ec94"

// cloudJobTemplate is the template of the user and group provisioning job;
// its job IDs start with "AD2AADProvisioning." (same page).
const cloudJobTemplate = "AD2AADProvisioning"

// provisionOnDemand's documented rate limit: "The rate limit for this API is
// 5 requests per 10 seconds."
// https://learn.microsoft.com/en-us/graph/api/synchronization-synchronizationjob-provisionondemand?view=graph-rest-1.0
const (
	provisionRate   = 5
	provisionPeriod = 10 * time.Second
)

// ErrProvisionFailed reports that on-demand provisioning ran but did not
// succeed; the error text carries Microsoft's step messages.
var ErrProvisionFailed = errors.New("on-demand provisioning did not succeed")

// Cloud drives Microsoft Entra Cloud Sync through Graph as the signed-in
// technician (design 6.2a): health from the provisioning job's status, and
// Sync now as provisionOnDemand for one object. The technician needs
// Synchronization.ReadWrite.All (delegated) and the Hybrid Identity
// Administrator role; finding the service principal also needs a delegated
// permission that can list service principals (Application.Read.All).
type Cloud struct {
	g GraphDoer

	// Now returns the current time; time.Now when nil.
	Now func() time.Time

	limiter rateLimiter

	mu    sync.Mutex
	rules map[string]string // service principal ID + "/" + job ID to rule ID
}

// NewCloud returns a Cloud Sync Syncer that calls Graph through g.
func NewCloud(g GraphDoer) *Cloud {
	c := &Cloud{g: g, rules: map[string]string{}}
	c.limiter = rateLimiter{n: provisionRate, per: provisionPeriod, now: c.now, sleep: sleepCtx}
	return c
}

var _ Syncer = (*Cloud)(nil)

func (c *Cloud) Kind() Kind { return KindCloud }

func (c *Cloud) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// Graph shapes, reduced to what is read. Times are strings so that null and
// placeholder values parse leniently.
type (
	servicePrincipal struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	}
	syncError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	taskExecution struct {
		State     string     `json:"state"`
		TimeBegan string     `json:"timeBegan"`
		TimeEnded string     `json:"timeEnded"`
		Error     *syncError `json:"error"`
	}
	quarantine struct {
		CurrentBegan string     `json:"currentBegan"`
		SeriesBegan  string     `json:"seriesBegan"`
		NextAttempt  string     `json:"nextAttempt"`
		Reason       string     `json:"reason"`
		Error        *syncError `json:"error"`
	}
	syncJob struct {
		ID         string `json:"id"`
		TemplateID string `json:"templateId"`
		Schedule   struct {
			Interval string `json:"interval"`
			State    string `json:"state"`
		} `json:"schedule"`
		Status struct {
			Code                            string         `json:"code"`
			CountSuccessiveCompleteFailures int64          `json:"countSuccessiveCompleteFailures"`
			LastExecution                   *taskExecution `json:"lastExecution"`
			Quarantine                      *quarantine    `json:"quarantine"`
		} `json:"status"`
	}
	syncRule struct {
		ID                  string `json:"id"`
		Name                string `json:"name"`
		SourceDirectoryName string `json:"sourceDirectoryName"`
		TargetDirectoryName string `json:"targetDirectoryName"`
	}
)

// cloudConfig is one Cloud Sync configuration: a service principal (named
// after the AD domain when created in the portal) and its provisioning job.
type cloudConfig struct {
	sp  servicePrincipal
	job syncJob
}

// configs lists the Cloud Sync configurations:
//
//	GET /servicePrincipals?$filter=applicationTemplateId eq '<CloudSyncTemplateID>'&$select=id,displayName
//	GET /servicePrincipals/{id}/synchronization/jobs
//
// https://learn.microsoft.com/en-us/graph/api/serviceprincipal-list?view=graph-rest-1.0
// https://learn.microsoft.com/en-us/graph/api/synchronization-synchronization-list-jobs?view=graph-rest-1.0
func (c *Cloud) configs(ctx context.Context) ([]cloudConfig, error) {
	if c.g == nil {
		return nil, ErrNotConfigured
	}
	filter := "applicationTemplateId eq '" + CloudSyncTemplateID + "'"
	path := "/servicePrincipals?$filter=" + queryEscape(filter) + "&$select=id,displayName"
	var sps struct {
		Value []servicePrincipal `json:"value"`
	}
	if err := c.g.Do(ctx, http.MethodGet, path, nil, &sps); err != nil {
		return nil, fmt.Errorf("find the Cloud Sync service principal: %w", err)
	}
	var out []cloudConfig
	for _, sp := range sps.Value {
		if sp.ID == "" {
			continue
		}
		var jobs struct {
			Value []syncJob `json:"value"`
		}
		if err := c.g.Do(ctx, http.MethodGet, "/servicePrincipals/"+url.PathEscape(sp.ID)+"/synchronization/jobs", nil, &jobs); err != nil {
			return nil, fmt.Errorf("read the Cloud Sync jobs of %s: %w", clean(sp.DisplayName, 100), err)
		}
		for _, j := range jobs.Value {
			if j.TemplateID == cloudJobTemplate || strings.HasPrefix(j.ID, cloudJobTemplate+".") {
				out = append(out, cloudConfig{sp: sp, job: j})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no Cloud Sync provisioning job was found in the tenant", ErrNotConfigured)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].sp.DisplayName) < strings.ToLower(out[j].sp.DisplayName)
	})
	return out, nil
}

// Health reports each Cloud Sync provisioning job: schedule, status code,
// last run and quarantine (synchronizationStatus,
// https://learn.microsoft.com/en-us/graph/api/resources/synchronization-synchronizationstatus?view=graph-rest-1.0).
// With several configurations the result is the most conservative: the
// oldest last run and the worst result, with warnings named per configuration.
func (c *Cloud) Health(ctx context.Context) (Health, error) {
	cfgs, err := c.configs(ctx)
	if err != nil {
		return Health{}, err
	}
	h := Health{Kind: KindCloud, SchedulerEnabled: true, CheckedAt: c.now()}
	var names []string
	worst := -1
	for _, cfg := range cfgs {
		name := clean(cfg.sp.DisplayName, 100)
		if name == "" {
			name = clean(cfg.sp.ID, 40)
		}
		names = append(names, name)
		warn := func(format string, a ...any) {
			msg := fmt.Sprintf(format, a...)
			if len(cfgs) > 1 {
				msg = name + ": " + msg
			}
			h.Warnings = append(h.Warnings, msg)
		}
		j := cfg.job
		if state := j.Schedule.State; !strings.EqualFold(state, "Active") {
			h.SchedulerEnabled = false
			warn("The provisioning schedule is %s.", orUnknown(clean(state, 40)))
		}
		if m := isoMinutes(j.Schedule.Interval); m > 0 && (h.IntervalMinutes == 0 || m < h.IntervalMinutes) {
			h.IntervalMinutes = m
		}
		switch code := clean(j.Status.Code, 40); code {
		case "Active":
		case "Quarantine":
			q := j.Status.Quarantine
			if q == nil {
				warn("The job is in quarantine.")
				break
			}
			since := ""
			if t := parseTime(q.SeriesBegan); t != nil {
				since = " since " + t.Format(time.RFC3339)
			}
			msg := fmt.Sprintf("The job is in quarantine%s (%s).", since, orUnknown(clean(q.Reason, 60)))
			if q.Error != nil && q.Error.Message != "" {
				msg += " " + clean(q.Error.Message, 400)
			}
			if t := parseTime(q.NextAttempt); t != nil {
				msg += " Next attempt " + t.Format(time.RFC3339) + "."
			}
			warn("%s", msg)
		case "Paused":
			warn("The job is paused.")
		case "NotRun":
			warn("The job has not completed its first run.")
		case "NotConfigured":
			warn("The job is not configured.")
		default:
			warn("Unrecognized job status %q.", code)
		}
		if n := j.Status.CountSuccessiveCompleteFailures; n > 0 {
			warn("The last %d runs failed.", n)
		}
		if e := j.Status.LastExecution; e != nil {
			t := parseTime(e.TimeEnded)
			if t == nil {
				t = parseTime(e.TimeBegan)
			}
			if t != nil && (h.LastCycle == nil || t.Before(*h.LastCycle)) {
				h.LastCycle = t
			}
			result, rank := cloudResult(e.State)
			if rank > worst {
				worst, h.LastResult = rank, result
			}
			if e.Error != nil && e.Error.Message != "" {
				warn("Last run: %s", clean(e.Error.Message, 400))
			}
		}
	}
	h.Server = strings.Join(names, ", ")
	return h, nil
}

// cloudResult maps synchronizationTaskExecution.state to Health.LastResult
// and a severity rank.
func cloudResult(state string) (string, int) {
	switch state {
	case "Succeeded":
		return "success", 0
	case "EntryLevelErrors":
		return "entry-level-errors", 1
	case "Failed":
		return "failed", 2
	case "":
		return "", -1
	default:
		return clean(state, 40), 1
	}
}

// SyncNow provisions obj on demand with Cloud Sync, exactly as Microsoft
// Learn's example 2 "Sync on-demand from Active Directory to Microsoft Entra
// ID (Microsoft Entra Cloud Sync)":
//
//	POST /servicePrincipals/{id}/synchronization/jobs/{jobId}/provisionOnDemand
//	{"parameters":[{"ruleId":"...","subjects":[{"objectId":"<distinguished name>","objectTypeName":"user"}]}]}
//
// https://learn.microsoft.com/en-us/graph/api/synchronization-synchronizationjob-provisionondemand?view=graph-rest-1.0
// objectId is the on-premises distinguished name, as synchronizationJobSubject
// documents for AD to Microsoft Entra ID
// (https://learn.microsoft.com/en-us/graph/api/resources/synchronization-synchronizationjobsubject?view=graph-rest-1.0).
// Calls are held to 5 per 10 seconds.
func (c *Cloud) SyncNow(ctx context.Context, obj ad.Object) (Result, error) {
	typ, err := cloudObjectType(obj)
	if err != nil {
		return Result{}, err
	}
	cfgs, err := c.configs(ctx)
	if err != nil {
		return Result{}, err
	}
	cfg, err := pickConfig(cfgs, obj.DN)
	if err != nil {
		return Result{}, err
	}
	rule, err := c.ruleID(ctx, cfg)
	if err != nil {
		return Result{}, err
	}
	type subject struct {
		ObjectID       string `json:"objectId"`
		ObjectTypeName string `json:"objectTypeName"`
	}
	type parameters struct {
		RuleID   string    `json:"ruleId"`
		Subjects []subject `json:"subjects"`
	}
	body := struct {
		Parameters []parameters `json:"parameters"`
	}{[]parameters{{RuleID: rule, Subjects: []subject{{ObjectID: obj.DN, ObjectTypeName: typ}}}}}

	if err := c.limiter.wait(ctx); err != nil {
		return Result{}, err
	}
	started := c.now()
	var resp struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	path := "/servicePrincipals/" + url.PathEscape(cfg.sp.ID) + "/synchronization/jobs/" + url.PathEscape(cfg.job.ID) + "/provisionOnDemand"
	if err := c.g.Do(ctx, http.MethodPost, path, body, &resp); err != nil {
		return Result{}, fmt.Errorf("provision %s on demand: %w", obj.DN, err)
	}
	msg, ok := provisionMessage(obj.DN, resp.Key, resp.Value)
	res := Result{Kind: KindCloud, Started: started, Message: msg}
	if !ok {
		return res, fmt.Errorf("%w: %s", ErrProvisionFailed, msg)
	}
	return res, nil
}

// cloudObjectType returns "user" or "group" for obj, the AD class names
// Cloud Sync provisions; computers and other classes are refused.
func cloudObjectType(obj ad.Object) (string, error) {
	if strings.TrimSpace(obj.DN) == "" {
		return "", errors.New("on-demand provisioning needs the object's distinguished name")
	}
	classes := map[string]bool{}
	for _, v := range obj.Values("objectClass") {
		classes[strings.ToLower(v)] = true
	}
	switch {
	case classes["computer"]:
		return "", errors.New("Cloud Sync does not provision computers on demand")
	case classes["group"]:
		return "group", nil
	case classes["user"] || classes["inetorgperson"]:
		return "user", nil
	}
	return "", errors.New("on-demand provisioning covers users and groups only")
}

// pickConfig chooses the configuration for dn: the only one, or the one
// whose name is dn's DNS domain.
func pickConfig(cfgs []cloudConfig, dn string) (cloudConfig, error) {
	if len(cfgs) == 1 {
		return cfgs[0], nil
	}
	domain := dnDomain(dn)
	for _, cfg := range cfgs {
		if domain != "" && strings.EqualFold(strings.TrimSpace(cfg.sp.DisplayName), domain) {
			return cfg, nil
		}
	}
	return cloudConfig{}, fmt.Errorf("%d Cloud Sync configurations exist and none is named %q", len(cfgs), domain)
}

// dnDomain returns the DNS domain of a distinguished name's DC components.
func dnDomain(dn string) string {
	var labels []string
	for _, rdn := range strings.Split(dn, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(rdn), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "DC") {
			labels = append(labels, strings.ToLower(strings.TrimSpace(v)))
		}
	}
	return strings.Join(labels, ".")
}

// ruleID reads the job's schema and returns the synchronization rule from
// Active Directory to Microsoft Entra ID; it is cached per job.
//
//	GET /servicePrincipals/{id}/synchronization/jobs/{jobId}/schema
//
// https://learn.microsoft.com/en-us/graph/api/synchronization-synchronizationschema-get?view=graph-rest-1.0
func (c *Cloud) ruleID(ctx context.Context, cfg cloudConfig) (string, error) {
	key := cfg.sp.ID + "/" + cfg.job.ID
	c.mu.Lock()
	id, ok := c.rules[key]
	c.mu.Unlock()
	if ok {
		return id, nil
	}
	var schema struct {
		Rules []syncRule `json:"synchronizationRules"`
	}
	path := "/servicePrincipals/" + url.PathEscape(cfg.sp.ID) + "/synchronization/jobs/" + url.PathEscape(cfg.job.ID) + "/schema"
	if err := c.g.Do(ctx, http.MethodGet, path, nil, &schema); err != nil {
		return "", fmt.Errorf("read the Cloud Sync schema: %w", err)
	}
	id = pickRule(schema.Rules)
	if id == "" {
		return "", errors.New("the Cloud Sync schema has no rule from Active Directory to Microsoft Entra ID")
	}
	c.mu.Lock()
	c.rules[key] = id
	c.mu.Unlock()
	return id, nil
}

func pickRule(rules []syncRule) string {
	for _, r := range rules {
		if r.ID != "" && isADDirectory(r.SourceDirectoryName) && isEntraDirectory(r.TargetDirectoryName) {
			return r.ID
		}
	}
	return ""
}

func isEntraDirectory(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "aad" || strings.Contains(n, "azure active directory") || strings.Contains(n, "entra")
}

func isADDirectory(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "ad" || (strings.Contains(n, "active directory") && !isEntraDirectory(n))
}

// provisionMessage turns the provisionOnDemand answer into readable text.
// key and value are JSON strings (stringKeyStringValuePair): key holds the
// result and details, value the provisioning steps. ok is false unless the
// result is Success or Skipped.
func provisionMessage(dn, key, value string) (string, bool) {
	var k struct {
		Result  string         `json:"result"`
		Details map[string]any `json:"details"`
	}
	var v struct {
		Steps []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Status      string `json:"status"`
			Description string `json:"description"`
		} `json:"provisioningSteps"`
		StatusInfo struct {
			Status            string `json:"status"`
			ErrorCode         string `json:"errorCode"`
			Reason            string `json:"reason"`
			RecommendedAction string `json:"recommendedAction"`
		} `json:"statusInfo"`
	}
	_ = json.Unmarshal([]byte(key), &k)
	_ = json.Unmarshal([]byte(value), &v)
	result := clean(k.Result, 40)
	if result == "" {
		result = clean(v.StatusInfo.Status, 40)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Cloud Sync on-demand provisioning of %s: %s.", dn, orUnknown(result))
	if s, _ := k.Details["errorMessage"].(string); s != "" {
		b.WriteString(" " + clean(s, 400))
	}
	for _, s := range v.Steps {
		step := clean(s.Type, 40)
		if step == "" {
			step = clean(s.Name, 60)
		}
		fmt.Fprintf(&b, " %s (%s): %s", step, clean(s.Status, 40), clean(s.Description, 300))
		if !strings.HasSuffix(b.String(), ".") {
			b.WriteString(".")
		}
	}
	if r := clean(v.StatusInfo.Reason, 300); r != "" {
		b.WriteString(" Reason: " + r + ".")
	}
	if a := clean(v.StatusInfo.RecommendedAction, 300); a != "" {
		b.WriteString(" Recommended action: " + a + ".")
	}
	ok := strings.EqualFold(result, "Success") || strings.EqualFold(result, "Skipped")
	return clean(b.String(), 2000), ok
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// queryEscape escapes a query value with %20 for spaces.
func queryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// isoMinutes reads an ISO 8601 duration such as PT40M or P1DT2H into whole
// minutes; 0 when it cannot.
func isoMinutes(s string) int {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !strings.HasPrefix(s, "P") {
		return 0
	}
	s = s[1:]
	var total float64
	inTime := false
	num := ""
	for _, r := range s {
		switch {
		case r == 'T':
			inTime = true
		case r >= '0' && r <= '9' || r == '.':
			num += string(r)
		default:
			n, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0
			}
			num = ""
			switch {
			case r == 'D' && !inTime:
				total += n * 24 * 60
			case r == 'W' && !inTime:
				total += n * 7 * 24 * 60
			case r == 'H' && inTime:
				total += n * 60
			case r == 'M' && inTime:
				total += n
			case r == 'S' && inTime:
				total += n / 60
			default:
				return 0
			}
		}
	}
	if num != "" {
		return 0
	}
	return int(total + 0.5)
}

// rateLimiter allows n calls in any period per.
type rateLimiter struct {
	mu    sync.Mutex
	n     int
	per   time.Duration
	times []time.Time
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

func (l *rateLimiter) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.mu.Lock()
		now := l.now()
		keep := l.times[:0]
		for _, t := range l.times {
			if now.Sub(t) < l.per {
				keep = append(keep, t)
			}
		}
		l.times = keep
		if len(l.times) < l.n {
			l.times = append(l.times, now)
			l.mu.Unlock()
			return nil
		}
		d := l.times[0].Add(l.per).Sub(now)
		l.mu.Unlock()
		if err := l.sleep(ctx, d); err != nil {
			return err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
