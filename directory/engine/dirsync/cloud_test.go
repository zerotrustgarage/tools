package dirsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// fakeGraph answers paths with canned JSON and records requests.
type fakeGraph struct {
	mu      sync.Mutex
	answers map[string]string
	errs    map[string]error
	calls   []string
	bodies  []string
}

func (f *fakeGraph) Do(ctx context.Context, method, path string, body any, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method+" "+path)
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		f.bodies = append(f.bodies, string(b))
	}
	if err := f.errs[path]; err != nil {
		return err
	}
	a, ok := f.answers[path]
	if !ok {
		return fmt.Errorf("unexpected %s %s", method, path)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(a), out)
}

const (
	spPath     = "/servicePrincipals?$filter=applicationTemplateId%20eq%20%271a4721b3-e57f-4451-ae87-ef078703ec94%27&$select=id,displayName"
	jobsPath   = "/servicePrincipals/sp1/synchronization/jobs"
	jobPrefix  = "/servicePrincipals/sp1/synchronization/jobs/AD2AADProvisioning.fc96887f36da47508c935c28a0c0b6da"
	schemaPath = jobPrefix + "/schema"
	podPath    = jobPrefix + "/provisionOnDemand"
)

const jobsAnswer = `{"value":[
 {"id":"AD2AADPasswordHash.aaa","templateId":"AD2AADPasswordHash","schedule":{"interval":"PT2M","state":"Active"},"status":{"code":"Active"}},
 {"id":"AD2AADProvisioning.fc96887f36da47508c935c28a0c0b6da","templateId":"ADDCInPassthrough",
  "schedule":{"expiration":null,"interval":"PT40M","state":"Active"},
  "status":{"code":"Active","countSuccessiveCompleteFailures":0,"quarantine":null,
   "lastExecution":{"state":"Succeeded","timeBegan":"2026-11-23T14:20:00Z","timeEnded":"2026-11-23T14:21:05.123Z","error":null}}}]}`

const schemaAnswer = `{"id":"x","synchronizationRules":[
 {"id":"wrong-way","name":"AAD to AD","sourceDirectoryName":"Azure Active Directory","targetDirectoryName":"Active Directory"},
 {"id":"6c409270-f78a-4bc6-af23-7cf3ab6482fe","name":"AD2AAD","sourceDirectoryName":"Active Directory","targetDirectoryName":"Azure Active Directory"}]}`

// podAnswer is Microsoft Learn's example 2 response, shortened.
const podAnswer = `{"@odata.context":"https://syncfabric.windowsazure.com/api/$metadata#microsoft.synchronization.stringKeyStringValuePair",
 "key":"{\"result\":\"Success\",\"details\":{}}",
 "value":"{\"provisioningSteps\":[{\"name\":\"EntryImportAdd\",\"type\":\"Import\",\"status\":\"Success\",\"description\":\"Received user 'adelev@chicago.com' change of type (Add) from Active Directory\",\"timestamp\":\"2022-04-21T18:40:07.8465145Z\",\"details\":{}},{\"name\":\"EntrySynchronizationScoping\",\"type\":\"Scoping\",\"status\":\"Success\",\"description\":\"Determine if user in scope by evaluating against each scoping filter\"},{\"name\":\"EntryExportUpdate\",\"type\":\"Export\",\"status\":\"Success\",\"description\":\"User 'adelev@chicago.com' was updated in Azure Active Directory\"}],\"statusInfo\":{\"status\":\"Success\"}}"}`

func newTestCloud() (*Cloud, *fakeGraph) {
	g := &fakeGraph{answers: map[string]string{
		spPath:     `{"value":[{"id":"sp1","displayName":"corp.chicago.com"}]}`,
		jobsPath:   jobsAnswer,
		schemaPath: schemaAnswer,
		podPath:    podAnswer,
	}, errs: map[string]error{}}
	c := NewCloud(g)
	c.Now = func() time.Time { return testNow }
	return c, g
}

var adele = ad.Object{DN: "CN=AdeleV,CN=Users,DC=corp,DC=chicago,DC=com",
	Attributes: map[string][]string{"objectclass": {"top", "person", "organizationalPerson", "user"}}}

func TestCloudHealth(t *testing.T) {
	c, g := newTestCloud()
	h, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if g.calls[0] != "GET "+spPath || g.calls[1] != "GET "+jobsPath {
		t.Fatalf("calls %q", g.calls)
	}
	if h.Kind != KindCloud || h.Server != "corp.chicago.com" || !h.SchedulerEnabled || h.IntervalMinutes != 40 ||
		h.LastResult != "success" || h.LastCycle == nil || !h.LastCycle.Equal(time.Date(2026, 11, 23, 14, 21, 5, 123e6, time.UTC)) ||
		!h.CheckedAt.Equal(testNow) || len(h.Warnings) != 0 || h.AppAuth != nil || h.Version != "" {
		t.Fatalf("health %+v", h)
	}
}

func TestCloudHealthQuarantine(t *testing.T) {
	c, g := newTestCloud()
	g.answers[spPath] = `{"value":[{"id":"sp1","displayName":"corp.chicago.com"},{"id":"sp2","displayName":"emea.chicago.com"}]}`
	g.answers["/servicePrincipals/sp2/synchronization/jobs"] = `{"value":[{"id":"AD2AADProvisioning.bbb","templateId":"AD2AADProvisioning",
	 "schedule":{"interval":"PT1H","state":"Disabled"},
	 "status":{"code":"Quarantine","countSuccessiveCompleteFailures":3,
	  "quarantine":{"reason":"EncounteredQuarantineException","seriesBegan":"2026-11-20T08:00:00Z","nextAttempt":"2026-11-23T20:00:00Z","error":{"code":"AgentUnreachable","message":"The provisioning agent did not respond."}},
	  "lastExecution":{"state":"Failed","timeBegan":"2026-11-20T08:00:00Z","timeEnded":"0001-01-01T00:00:00Z","error":{"message":"Agent down"}}}}]}`
	h, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Server != "corp.chicago.com, emea.chicago.com" || h.SchedulerEnabled || h.IntervalMinutes != 40 || h.LastResult != "failed" ||
		h.LastCycle == nil || !h.LastCycle.Equal(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("health %+v", h)
	}
	all := strings.Join(h.Warnings, "\n")
	for _, want := range []string{"emea.chicago.com: The provisioning schedule is Disabled", "quarantine since 2026-11-20T08:00:00Z (EncounteredQuarantineException)",
		"did not respond", "last 3 runs failed", "Last run: Agent down"} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning with %q in:\n%s", want, all)
		}
	}
}

func TestCloudNotConfigured(t *testing.T) {
	c, g := newTestCloud()
	g.answers[spPath] = `{"value":[]}`
	if _, err := c.Health(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("no service principal: %v", err)
	}
	g.answers[spPath] = `{"value":[{"id":"sp1","displayName":"x"}]}`
	g.answers[jobsPath] = `{"value":[{"id":"AD2AADPasswordHash.a","templateId":"AD2AADPasswordHash"}]}`
	if _, err := c.SyncNow(context.Background(), adele); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("no provisioning job: %v", err)
	}
	forbidden := errors.New("forbidden")
	g.errs[spPath] = forbidden
	if _, err := c.Health(context.Background()); !errors.Is(err, forbidden) {
		t.Fatalf("graph error: %v", err)
	}
	if _, err := NewCloud(nil).Health(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil graph: %v", err)
	}
}

func TestCloudSyncNow(t *testing.T) {
	c, g := newTestCloud()
	res, err := c.SyncNow(context.Background(), adele)
	if err != nil {
		t.Fatal(err)
	}
	if g.calls[len(g.calls)-1] != "POST "+podPath || g.calls[len(g.calls)-2] != "GET "+schemaPath {
		t.Fatalf("calls %q", g.calls)
	}
	want := `{"parameters":[{"ruleId":"6c409270-f78a-4bc6-af23-7cf3ab6482fe","subjects":[{"objectId":"CN=AdeleV,CN=Users,DC=corp,DC=chicago,DC=com","objectTypeName":"user"}]}]}`
	if g.bodies[0] != want {
		t.Fatalf("body\n%s\nwant\n%s", g.bodies[0], want)
	}
	if res.Kind != KindCloud || !res.Started.Equal(testNow) {
		t.Fatalf("result %+v", res)
	}
	for _, w := range []string{"of CN=AdeleV,CN=Users,DC=corp,DC=chicago,DC=com: Success.", "Import (Success): Received user 'adelev@chicago.com'",
		"Export (Success): User 'adelev@chicago.com' was updated in Azure Active Directory."} {
		if !strings.Contains(res.Message, w) {
			t.Errorf("message %q lacks %q", res.Message, w)
		}
	}

	// The rule is cached: a second call reads no schema.
	n := len(g.calls)
	group := ad.Object{DN: "CN=Sales,OU=Groups,DC=corp,DC=chicago,DC=com", Attributes: map[string][]string{"objectclass": {"top", "group"}}}
	if _, err := c.SyncNow(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	for _, call := range g.calls[n:] {
		if strings.HasSuffix(call, "/schema") {
			t.Fatal("schema read again")
		}
	}
	if !strings.Contains(g.bodies[1], `"objectTypeName":"group"`) {
		t.Fatalf("group body %s", g.bodies[1])
	}
}

func TestCloudSyncNowOutcomes(t *testing.T) {
	c, g := newTestCloud()
	g.answers[podPath] = `{"key":"{\"result\":\"Skipped\",\"details\":{\"errorCode\":\"RedundantExport\",\"errorMessage\":\"The state of the user in both the source and target systems already match.\"}}","value":"{}"}`
	res, err := c.SyncNow(context.Background(), adele)
	if err != nil || !strings.Contains(res.Message, "Skipped. The state of the user") {
		t.Fatalf("skipped: %v %q", err, res.Message)
	}

	g.answers[podPath] = `{"key":"{\"result\":\"Failure\",\"details\":{}}","value":"{\"provisioningSteps\":[{\"type\":\"Scoping\",\"status\":\"Skipped\",\"description\":\"The user is out of scope\"}],\"statusInfo\":{\"status\":\"Failure\",\"reason\":\"Out of scope\\u0007\",\"recommendedAction\":\"Check the scoping filter\"}}"}`
	res, err = c.SyncNow(context.Background(), adele)
	if !errors.Is(err, ErrProvisionFailed) || !strings.Contains(res.Message, "Scoping (Skipped): The user is out of scope.") ||
		!strings.Contains(res.Message, "Reason: Out of scope.") || !strings.Contains(err.Error(), "Check the scoping filter") {
		t.Fatalf("failure: %v %q", err, res.Message)
	}

	for _, obj := range []ad.Object{
		{DN: "CN=PC1,DC=corp,DC=chicago,DC=com", Attributes: map[string][]string{"objectclass": {"user", "computer"}}},
		{DN: "CN=Ct,DC=corp,DC=chicago,DC=com", Attributes: map[string][]string{"objectclass": {"contact"}}},
		{Attributes: map[string][]string{"objectclass": {"user"}}},
	} {
		if _, err := c.SyncNow(context.Background(), obj); err == nil {
			t.Errorf("%q accepted", obj.DN)
		}
	}

	g.answers[schemaPath] = `{"synchronizationRules":[{"id":"r","sourceDirectoryName":"Azure Active Directory","targetDirectoryName":"Active Directory"}]}`
	c2 := NewCloud(g)
	if _, err := c2.SyncNow(context.Background(), adele); err == nil || !strings.Contains(err.Error(), "no rule") {
		t.Fatalf("no rule: %v", err)
	}
}

func TestCloudPickConfig(t *testing.T) {
	c, g := newTestCloud()
	g.answers[spPath] = `{"value":[{"id":"sp2","displayName":"emea.chicago.com"},{"id":"sp1","displayName":"CORP.chicago.com"}]}`
	g.answers["/servicePrincipals/sp2/synchronization/jobs"] = `{"value":[{"id":"AD2AADProvisioning.bbb","templateId":"AD2AADProvisioning"}]}`
	if _, err := c.SyncNow(context.Background(), adele); err != nil {
		t.Fatal(err)
	}
	if g.calls[len(g.calls)-1] != "POST "+podPath {
		t.Fatalf("wrong configuration: %q", g.calls[len(g.calls)-1])
	}
	other := ad.Object{DN: "CN=X,DC=apac,DC=chicago,DC=com", Attributes: map[string][]string{"objectclass": {"user"}}}
	if _, err := c.SyncNow(context.Background(), other); err == nil {
		t.Fatal("object outside every configuration accepted")
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	var slept []time.Duration
	l := rateLimiter{n: 5, per: 10 * time.Second, now: func() time.Time { return now },
		sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); now = now.Add(d); return nil }}
	for i := 0; i < 5; i++ {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	if len(slept) != 0 {
		t.Fatalf("slept early: %v", slept)
	}
	if err := l.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != 5*time.Second {
		t.Fatalf("slept %v, want 5s", slept)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}

	// The Cloud wires the documented limit.
	c := NewCloud(&fakeGraph{})
	if c.limiter.n != 5 || c.limiter.per != 10*time.Second {
		t.Fatal("provisionOnDemand limit not 5 per 10 seconds")
	}
}

func TestIsoMinutes(t *testing.T) {
	for in, want := range map[string]int{"PT40M": 40, "PT2M": 2, "PT1H30M": 90, "P1D": 1440, "P1DT1H": 1500, "PT90S": 2, "pt40m": 40,
		"": 0, "40": 0, "PT": 0, "PTM": 0, "P1M": 0, "PT40": 0} {
		if got := isoMinutes(in); got != want {
			t.Errorf("%q = %d, want %d", in, got, want)
		}
	}
}

func TestDirectoryNames(t *testing.T) {
	if !isADDirectory("Active Directory") || isADDirectory("Azure Active Directory") || isADDirectory("Microsoft Entra ID") ||
		!isEntraDirectory("Azure Active Directory") || !isEntraDirectory("Microsoft Entra ID") || isEntraDirectory("Active Directory") {
		t.Fatal("directory names misclassified")
	}
	if dnDomain("CN=A\\,B,OU=X,DC=Corp,DC=Example,DC=com") != "corp.example.com" {
		t.Fatal(dnDomain("CN=A\\,B,OU=X,DC=Corp,DC=Example,DC=com"))
	}
}
