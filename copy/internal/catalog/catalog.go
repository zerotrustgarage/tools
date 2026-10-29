// Package catalog supplies the shared option reference for CLI and desktop designers.
package catalog

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed options.json
var data []byte

type Option struct {
	Token         string   `json:"token"`
	Syntax        string   `json:"syntax"`
	Group         string   `json:"group"`
	Argument      string   `json:"argument"`
	Type          string   `json:"type"`
	Help          string   `json:"help"`
	Handling      string   `json:"handling"`
	Tags          []string `json:"tags"`
	ConflictsWith []string `json:"conflicts_with"`
	Implies       []string `json:"implies"`
	Requires      []string `json:"requires"`
}

var options = func() []Option {
	var v []Option
	if err := json.Unmarshal(data, &v); err != nil {
		panic(err)
	}
	return v
}()

// All returns independent values; callers cannot change the embedded catalog.
func All() []Option {
	var result []Option
	if err := json.Unmarshal(data, &result); err != nil {
		panic(err)
	}
	return result
}

func Lookup(token string) (Option, bool) {
	token = strings.ToUpper(strings.SplitN(token, ":", 2)[0])
	for _, o := range options {
		if o.Token == token {
			return o, true
		}
	}
	return Option{}, false
}

func Search(query string) []Option {
	query = strings.ToLower(query)
	result := make([]Option, 0)
	for _, o := range All() {
		if strings.Contains(strings.ToLower(o.Syntax+" "+o.Help+" "+o.Handling+" "+o.Group+" "+strings.Join(o.Tags, " ")), query) {
			result = append(result, o)
		}
	}
	return result
}
