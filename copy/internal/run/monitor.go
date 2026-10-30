package run

import (
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/engine"
	"github.com/zerotrustgarage/tools/copy/internal/metrics"
	"github.com/zerotrustgarage/tools/copy/internal/parse"
)

// Options changes how Execute observes a run. The zero value is the product setting.
type Options struct {
	// Clock drives the metrics; nil is the system clock.
	Clock metrics.Clock
	// PollEvery is how often the live native log is read and a reading published;
	// zero means engine.SampleInterval (500 ms, design section 6).
	PollEvery time.Duration
}

// metricsObserver is implemented by progress writers that show live readings. It is
// called from the monitor's goroutine, so it must be safe for concurrent use.
type metricsObserver interface {
	MetricsUpdated(phase string, s metrics.Snapshot)
}

// monitor follows one engine phase: it reads the native Unicode log as it grows,
// takes the engine's I/O readings and publishes a snapshot on every poll.
type monitor struct {
	tracker *metrics.Tracker
	tail    *metrics.Tailer
	publish func(metrics.Snapshot)

	mu       sync.Mutex
	tailErr  error
	stop     chan struct{}
	done     chan struct{}
	finished bool
}

func newMonitor(cfg metrics.Config, logPath string, publish func(metrics.Snapshot)) *monitor {
	if publish == nil {
		publish = func(metrics.Snapshot) {}
	}
	return &monitor{tracker: metrics.NewTracker(cfg), tail: metrics.NewTailer(logPath), publish: publish}
}

// step reads what the log gained, lets time pass and publishes. The goroutine calls
// it on a timer; tests call it directly.
func (m *monitor) step() {
	err := m.tail.Poll(m.tracker.Log)
	m.mu.Lock()
	if err != nil && m.tailErr == nil {
		m.tailErr = err
	}
	m.mu.Unlock()
	m.tracker.Tick()
	m.publish(m.tracker.Snapshot())
}

func (m *monitor) start(every time.Duration) {
	if every <= 0 {
		every = engine.SampleInterval
	}
	m.stop, m.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-ticker.C:
				m.step()
			}
		}
	}()
}

// io takes one reading of the engine's process counters.
func (m *monitor) io(c engine.IOCounters) {
	m.tracker.IO(metrics.IOSample{ReadBytes: c.ReadBytes})
}

// finish stops the goroutine, reads the rest of the log and closes the phase. A
// summary, when one was read, supplies the final totals.
func (m *monitor) finish(summary *parse.Summary) metrics.Final {
	if m.stop != nil && !m.finished {
		close(m.stop)
		<-m.done
	}
	m.finished = true
	if err := m.tail.Poll(m.tracker.Log); err != nil {
		m.mu.Lock()
		if m.tailErr == nil {
			m.tailErr = err
		}
		m.mu.Unlock()
	}
	final := m.tracker.Finish(summary)
	m.mu.Lock()
	if m.tailErr != nil {
		final.Limits = append(final.Limits, "The native log could not be read while it was being written ("+m.tailErr.Error()+"); live readings used the I/O counters only.")
	}
	m.mu.Unlock()
	m.publish(m.tracker.Snapshot())
	return final
}
