package metrics

// Snapshot is the live reading handed to the CLI and the desktop Activity view.
// Optional readings are null when the signals cannot support them, never zero.
type Snapshot struct {
	Phase string `json:"phase"`
	// Status is starting, active, retrying, stalled or finished. Stalled is a
	// status, not a failure.
	Status     string `json:"status"`
	StatusNote string `json:"status_note,omitempty"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	// QuietMS is the time since the last progress signal, not counting a retry wait.
	QuietMS    int64 `json:"quiet_ms"`
	Concurrent bool  `json:"concurrent"`
	ListOnly   bool  `json:"list_only"`

	Completed Completed `json:"completed"`
	Seen      Seen      `json:"seen"`
	Current   *Current  `json:"current,omitempty"`
	Rate      Rates     `json:"rate"`
	Activity  Activity  `json:"activity"`

	Progress     *Progress `json:"progress,omitempty"`
	ProgressNote string    `json:"progress_note,omitempty"`
	ETA          *ETA      `json:"eta,omitempty"`
	ETANote      string    `json:"eta_note,omitempty"`

	// ErrorLines counts native-log error headers, including retries.
	ErrorLines uint64 `json:"error_lines"`
}

// Completed counts finished files. Known is false when the output cannot say
// which files finished; the numbers are then zero and mean nothing.
type Completed struct {
	Known bool   `json:"known"`
	Files uint64 `json:"files"`
	Bytes uint64 `json:"bytes"`
	Note  string `json:"note,omitempty"`
}

// Seen counts what Robocopy announced. Announcements are not completion.
type Seen struct {
	Files uint64 `json:"files"`
	Bytes uint64 `json:"bytes"`
	Dirs  uint64 `json:"dirs"`
}

// Current is the last announced file. Under /MT it is only the latest line.
type Current struct {
	Path      string `json:"path"`
	Size      uint64 `json:"size"`
	Announced bool   `json:"announced"`
	// Percent is Robocopy's own marker for this file (serial copies only). It is
	// advisory and never added to completed bytes.
	Percent *int `json:"percent,omitempty"`
}

// Rates are measured from completed files. All are bytes (or files) per second.
type Rates struct {
	// CompletedBytesPerSec is the mean over the sliding window (WindowSeconds).
	CompletedBytesPerSec *uint64 `json:"completed_bytes_per_sec,omitempty"`
	// AverageBytesPerSec is completed bytes over phase time.
	AverageBytesPerSec *uint64 `json:"average_bytes_per_sec,omitempty"`
	// PeakBytesPerSec is the highest full-window rate seen.
	PeakBytesPerSec *uint64  `json:"peak_bytes_per_sec,omitempty"`
	FilesPerSec     *float64 `json:"files_per_sec,omitempty"`
	WindowSeconds   int      `json:"window_seconds"`
}

// Activity is the separate transfer-activity signal from process I/O counters.
// It is never shown as completed.
type Activity struct {
	Available          bool    `json:"available"`
	ReadBytes          uint64  `json:"read_bytes"`
	BytesPerSec        *uint64 `json:"bytes_per_sec,omitempty"`
	AverageBytesPerSec *uint64 `json:"average_bytes_per_sec,omitempty"`
	PeakBytesPerSec    *uint64 `json:"peak_bytes_per_sec,omitempty"`
	Note               string  `json:"note,omitempty"`
}

// Progress is present only when finished files are countable and a preview gave
// totals to compare with.
type Progress struct {
	PlannedFiles uint64   `json:"planned_files"`
	PlannedBytes uint64   `json:"planned_bytes"`
	BytesPercent *float64 `json:"bytes_percent,omitempty"`
	FilesPercent *float64 `json:"files_percent,omitempty"`
	// ExceededPlan means more finished than the preview counted: the source changed.
	ExceededPlan bool `json:"exceeded_plan,omitempty"`
}

// ETA is a time remaining, in seconds, with the range it could fall in.
type ETA struct {
	Seconds     int64  `json:"seconds"`
	LowSeconds  int64  `json:"low_seconds"`
	HighSeconds int64  `json:"high_seconds"`
	Steady      bool   `json:"steady"`
	Basis       string `json:"basis"`
}

// Point is one entry of the down-sampled timeline.
type Point struct {
	TMS            int64  `json:"t_ms"`
	Status         string `json:"status"`
	CompletedBytes uint64 `json:"completed_bytes"`
	CompletedFiles uint64 `json:"completed_files"`
	ReadBytes      uint64 `json:"read_bytes"`
	// Rates are over the sliding window at that time, bytes per second.
	CompletedBytesPerSec uint64 `json:"completed_bytes_per_sec"`
	ActivityBytesPerSec  uint64 `json:"activity_bytes_per_sec"`
}

// Totals is a count of files and bytes.
type Totals struct {
	Files uint64 `json:"files"`
	Bytes uint64 `json:"bytes"`
}

// Stall is one stretch with no progress signal, outside retry waits.
type Stall struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
}

// Final is the record of one phase kept in run.json (design section 10).
type Final struct {
	Phase     string `json:"phase"`
	ElapsedMS int64  `json:"elapsed_ms"`
	// Source says where Completed comes from: "summary" (Robocopy's positional
	// totals), "log" (finished-file accounting when no summary exists) or "none".
	Source string `json:"source"`
	// Completed is only set for a copy phase.
	Completed *Totals `json:"completed,omitempty"`
	// LogCompleted is what live accounting had counted when the summary arrived, kept
	// so the accounting can be compared with the authoritative totals.
	LogCompleted *Totals `json:"log_completed,omitempty"`
	Seen         Seen    `json:"seen"`

	AverageBytesPerSec *uint64  `json:"average_bytes_per_sec,omitempty"`
	PeakBytesPerSec    *uint64  `json:"peak_bytes_per_sec,omitempty"`
	FilesPerSecAverage *float64 `json:"files_per_sec_average,omitempty"`

	ActivityAvailable          bool    `json:"activity_available"`
	ActivityReadBytes          uint64  `json:"activity_read_bytes"`
	ActivityAverageBytesPerSec *uint64 `json:"activity_average_bytes_per_sec,omitempty"`
	ActivityPeakBytesPerSec    *uint64 `json:"activity_peak_bytes_per_sec,omitempty"`

	ErrorLines        uint64   `json:"error_lines"`
	Stalls            []Stall  `json:"stalls,omitempty"`
	StallsOmitted     int      `json:"stalls_omitted,omitempty"`
	StalledMS         int64    `json:"stalled_ms"`
	Concurrent        bool     `json:"concurrent"`
	ListOnly          bool     `json:"list_only"`
	ProgressAvailable bool     `json:"progress_available"`
	Limits            []string `json:"limits,omitempty"`
	WindowSeconds     int      `json:"window_seconds"`
	StallAfterSecs    int      `json:"stall_after_seconds"`
	Timeline          []Point  `json:"timeline"`
}
