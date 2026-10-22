package guard

import (
	"testing"

	"github.com/zerotrustgarage/tools/copy/internal/job"
)

func TestPolicyRefusesBypasses(t *testing.T) {
	base := job.Job{Schema: 1, Source: `C:\source`, Destination: `D:\destination`}
	for _, a := range []string{"/MIR", "/PURGE", "/MOVE", "/MOV", "/M", "/REG", "/JOB:other", "/SAVE:other", "/NJS", "/UNILOG:other", "/L", "/MON:1", "/COPYALL", "/COPY:DATS", "/DCOPY:E", "/FUTURE"} {
		j := base
		j.Options = []string{a}
		if Policy(j) == nil {
			t.Errorf("allowed %s", a)
		}
	}
	base.Options = []string{"/MT:8", "/R:0", "/XF", "*.tmp", "*.bak", "/XD", "cache"}
	if err := Policy(base); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{`C:\data`, `c:\DATA\child`}, {`C:\data\child`, `C:\data`}, {`C:\data\`, `C:\data`}} {
		if !Overlap(pair[0], pair[1]) {
			t.Fatalf("missed overlap %q", pair)
		}
	}
	if Overlap(`C:\data`, `C:\database`) {
		t.Fatal("confused sibling names")
	}
}
