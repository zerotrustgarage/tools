// Package oui maps MAC addresses to the organization the IEEE Registration
// Authority assigned the prefix to. The database is generated from the
// public IEEE listings (MA-L, MA-M, MA-S and IAB) by ./gen and embedded.
package oui

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"net"
	"strings"
	"sync"
)

//go:generate go run ./gen -out data/oui.txt.gz

//go:embed data/oui.txt.gz
var embedded []byte

// Info describes a MAC address.
type Info struct {
	// Vendor is the IEEE assignee, empty when unknown.
	Vendor string `json:"vendor,omitempty"`
	// Randomized is true for locally administered addresses: phones,
	// laptops and virtual machines that do not use their burned-in MAC.
	// Such addresses have no IEEE assignee.
	Randomized bool `json:"randomized,omitempty"`
	// Multicast is true for group addresses, which never belong to a host.
	Multicast bool `json:"multicast,omitempty"`
}

// DB is an in-memory prefix database.
type DB struct {
	byLen map[int]map[string]string // hex prefix length (6, 7, 9) -> prefix -> vendor
	count int
}

var (
	defaultOnce sync.Once
	defaultDB   *DB
	defaultErr  error
)

// Default returns the embedded database, loading it on first use.
func Default() (*DB, error) {
	defaultOnce.Do(func() {
		defaultDB, defaultErr = Load(bytes.NewReader(embedded))
	})
	return defaultDB, defaultErr
}

// Load reads a gzip-compressed "PREFIX<TAB>Vendor" list.
func Load(r interface{ Read([]byte) (int, error) }) (*DB, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("oui: %w", err)
	}
	defer zr.Close()
	db := &DB{byLen: map[int]map[string]string{}}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		prefix, vendor, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		m := db.byLen[len(prefix)]
		if m == nil {
			m = map[string]string{}
			db.byLen[len(prefix)] = m
		}
		m[prefix] = vendor
		db.count++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("oui: %w", err)
	}
	return db, nil
}

// Len reports the number of prefixes loaded.
func (db *DB) Len() int { return db.count }

// Lookup describes mac. It accepts any form net.ParseMAC accepts.
func (db *DB) Lookup(mac string) (Info, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return Info{}, fmt.Errorf("oui: %q is not a 48-bit MAC address", mac)
	}
	return db.LookupHW(hw), nil
}

// LookupHW describes a parsed 48-bit hardware address.
func (db *DB) LookupHW(hw net.HardwareAddr) Info {
	var info Info
	if len(hw) != 6 {
		return info
	}
	info.Multicast = hw[0]&0x01 != 0
	info.Randomized = hw[0]&0x02 != 0
	if info.Randomized || info.Multicast {
		return info
	}
	hex := strings.ToUpper(fmt.Sprintf("%x", []byte(hw)))
	for _, n := range []int{9, 7, 6} { // longest assignment first
		if v, ok := db.byLen[n][hex[:n]]; ok {
			info.Vendor = v
			break
		}
	}
	return info
}
