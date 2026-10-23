package audit

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

type Record struct {
	Time              time.Time         `json:"time"`
	Session           string            `json:"session"`
	Transfer          string            `json:"transfer"`
	Op                string            `json:"op"`
	Source            string            `json:"source"`
	Local             string            `json:"local"`
	MappedName        string            `json:"mapped_name,omitempty"`
	Name              string            `json:"name"`
	Options           map[string]string `json:"options,omitempty"`
	Bytes             int64             `json:"bytes"`
	SourceSHA256      string            `json:"source_sha256,omitempty"`
	SHA256            string            `json:"sha256,omitempty"`
	DurationMS        int64             `json:"duration_ms"`
	Result            string            `json:"result"`
	Reason            string            `json:"reason,omitempty"`
	Suppressed        int               `json:"suppressed,omitempty"`
	Protocol          string            `json:"protocol"`
	StoredAs          string            `json:"stored_as,omitempty"`
	Findings          []string          `json:"findings,omitempty"`
	FindingsTruncated bool              `json:"findings_truncated,omitempty"`
	ChangedSince      string            `json:"changed_since,omitempty"`
	Changed           *bool             `json:"changed,omitempty"`
}

type Log struct {
	mu  sync.Mutex
	enc *json.Encoder
	err error
}

func New(w io.Writer) *Log { return &Log{enc: json.NewEncoder(w)} }
func (l *Log) Write(r Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	r.Time = time.Now().UTC()
	r.Protocol = "TFTP: no authentication or encryption; never use for CUI"
	l.err = l.enc.Encode(r)
	return l.err
}
func (l *Log) Err() error { l.mu.Lock(); defer l.mu.Unlock(); return l.err }
