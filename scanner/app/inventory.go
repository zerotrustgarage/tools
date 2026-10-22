package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/inventory"
	"github.com/zerotrustgarage/tools/scanner/internal/report"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

func (a *App) document(all bool) report.Document {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.documentLocked(all)
}

func (a *App) documentLocked(all bool) report.Document {
	d := report.Document{Tool: "ztg-scanner-desktop", Version: version, Site: a.site,
		Targets: append([]string(nil), a.lastReq...), Annotations: map[string]scan.Annotation{}}
	for _, h := range a.hosts {
		if all || h.Status == "up" {
			d.Hosts = append(d.Hosts, h)
		}
	}
	for key, note := range a.annotations {
		d.Annotations[key] = note
	}
	if a.summary != nil {
		d.Summary = *a.summary
	}
	report.SortHosts(d.Hosts)
	return d
}

// SetAnnotation edits only a device currently shown by the engine or a loaded
// inventory. Settings and exports contain no command templates or credentials.
func (a *App) SetAnnotation(ip string, note scan.Annotation) error {
	if err := inventory.ValidateAnnotation(note); err != nil {
		return err
	}
	note.Tags = append([]string(nil), note.Tags...)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return errors.New("wait for the scan to finish before editing inventory")
	}
	for i := range a.hosts {
		h := &a.hosts[i]
		if h.IP != ip {
			continue
		}
		if h.Status != "up" && h.LastKnownMAC == "" {
			return errors.New("edit inventory from a responsive device")
		}
		key := inventory.AnnotationKey(*h)
		if _, exists := a.annotations[key]; !exists && len(a.annotations) >= targets.DefaultMaxHosts {
			return errors.New("inventory annotation limit reached")
		}
		if a.annotations == nil {
			a.annotations = map[string]scan.Annotation{}
		}
		a.annotations[key] = note
		h.Annotation = note
		a.revision++
		return nil
	}
	return errors.New("that device is not in the results")
}

func (a *App) SetSite(site string) error {
	site = strings.TrimSpace(site)
	if len(site) > 200 || strings.ContainsAny(site, "\r\n\x00") {
		return errors.New("site label must be at most 200 bytes on one line")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return errors.New("wait for the scan to finish")
	}
	if a.site != site {
		a.revision++
	}
	a.site = site
	return nil
}

// SaveScan saves every visited address, regardless of grid filters, so the file
// remains useful as a comparison baseline. Saving is always an explicit action.
func (a *App) SaveScan(site string) (string, error) {
	if a.platform == nil {
		return "", errors.New("saving is not available")
	}
	if err := a.SetSite(site); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return "", errors.New("wait for the scan to finish")
	}
	d, revision := a.documentLocked(true), a.revision
	a.mu.Unlock()
	if len(d.Hosts) == 0 {
		return "", errors.New("there is no scan to save")
	}
	path, err := a.platform.SaveFile("ztg-scan-"+time.Now().Format("2006-01-02-150405")+".json", "ZTG scan (*.json)", "*.json")
	if err != nil || path == "" {
		return "", err
	}
	if err := report.WriteFile(path, func(w io.Writer) error { return report.JSON(w, d) }); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.revision == revision {
		a.savedRevision = revision
		a.filename = path
	}
	a.mu.Unlock()
	return path, nil
}

func (a *App) readScan(title string) (report.Document, string, error) {
	if a.platform == nil {
		return report.Document{}, "", errors.New("opening files is not available")
	}
	a.mu.Lock()
	running := a.running
	a.mu.Unlock()
	if running {
		return report.Document{}, "", errors.New("wait for the scan to finish")
	}
	path, err := a.platform.OpenFile(title, "ZTG scan (*.json)", "*.json")
	if err != nil || path == "" {
		return report.Document{}, "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return report.Document{}, "", err
	}
	defer f.Close()
	d, err := report.Read(f)
	return d, path, err
}

// LoadScan reads data only. It never authorizes public targets or starts probes.
func (a *App) LoadScan() (string, error) {
	d, path, err := a.readScan("Open a saved scan")
	if err != nil || path == "" {
		return "", err
	}
	notes := d.Annotations
	if notes == nil {
		notes = map[string]scan.Annotation{}
	}
	for _, h := range d.Hosts {
		if h.Notes != "" || len(h.Tags) > 0 || h.Favorite {
			notes[inventory.AnnotationKey(h)] = h.Annotation
		}
	}
	if len(notes) > targets.DefaultMaxHosts {
		return "", fmt.Errorf("too many inventory annotations")
	}
	inventory.ApplyAnnotations(nil, d.Hosts, notes)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return "", errors.New("a scan started while choosing a file")
	}
	a.hosts, a.lastReq, a.site, a.summary = d.Hosts, d.Targets, d.Site, &d.Summary
	a.allowPub = false
	a.annotations = notes
	a.revision++
	a.savedRevision = a.revision
	a.filename = path
	return path, nil
}

func (a *App) NewSite() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return errors.New("wait for the scan to finish")
	}
	a.hosts, a.summary, a.lastReq, a.site, a.allowPub = nil, nil, nil, "", false
	a.previous = nil
	a.filename = ""
	a.revision++
	a.savedRevision = a.revision
	a.annotations = map[string]scan.Annotation{}
	return nil
}

// UnsavedChanges also guards the native close button and Alt+F4.
func (a *App) UnsavedChanges() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.revision != a.savedRevision
}

type Comparison struct {
	Path    string             `json:"path"`
	Changes []inventory.Change `json:"changes"`
}

func (a *App) CompareScan(site string) (Comparison, error) {
	if err := a.SetSite(site); err != nil {
		return Comparison{}, err
	}
	baseline, path, err := a.readScan("Choose the baseline scan")
	if err != nil || path == "" {
		return Comparison{}, err
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return Comparison{}, errors.New("wait for the scan to finish")
	}
	d := a.documentLocked(true)
	a.mu.Unlock()
	if len(d.Hosts) == 0 {
		return Comparison{}, errors.New("scan or open a current inventory first")
	}
	if baseline.Site != "" && d.Site != "" && !strings.EqualFold(baseline.Site, d.Site) {
		return Comparison{}, errors.New("the baseline belongs to a different site")
	}
	return Comparison{Path: path, Changes: inventory.Compare(baseline.Hosts, d.Hosts, d.Summary.Canceled || d.Summary.Finished.IsZero())}, nil
}
