// Package bits designs PowerShell BITS commands without running transfers.
package bits

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed options.json
var catalogJSON []byte

type Entry struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Values       []string `json:"values"`
	Group        string   `json:"group"`
	Help         string   `json:"help"`
	DefaultValue string   `json:"default_value"`
	Available    bool     `json:"available"`
}

func Catalog() []Entry {
	var entries []Entry
	if err := json.Unmarshal(catalogJSON, &entries); err != nil {
		panic(err)
	}
	return entries
}

func lookup(name string) (Entry, bool) {
	for _, entry := range Catalog() {
		if strings.EqualFold(name, entry.Name) {
			return entry, true
		}
	}
	return Entry{}, false
}
