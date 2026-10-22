// Command gen rebuilds the embedded MAC vendor database from the IEEE
// Registration Authority's public listings.
//
//	go generate ./internal/oui
//
// IEEE's front end refuses requests without browser-like headers, so the
// request carries them. The output is sorted so regenerating with unchanged
// upstream data produces an identical file.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

var sources = []string{
	"https://standards-oui.ieee.org/oui/oui.csv",     // MA-L, 24-bit
	"https://standards-oui.ieee.org/oui28/mam.csv",   // MA-M, 28-bit
	"https://standards-oui.ieee.org/oui36/oui36.csv", // MA-S, 36-bit
	"https://standards-oui.ieee.org/iab/iab.csv",     // IAB, 36-bit (legacy)
}

func main() {
	out := flag.String("out", "data/oui.txt.gz", "output file")
	flag.Parse()

	client := &http.Client{Timeout: 3 * time.Minute}
	entries := map[string]string{}
	for _, url := range sources {
		n, err := fetch(client, url, entries)
		if err != nil {
			log.Fatalf("%s: %v", url, err)
		}
		log.Printf("%s: %d assignments", url, n)
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Header.ModTime = time.Unix(0, 0) // reproducible output
	fmt.Fprintf(zw, "# IEEE Registration Authority public listing (MA-L, MA-M, MA-S, IAB). %d prefixes.\n", len(keys))
	for _, k := range keys {
		fmt.Fprintf(zw, "%s\t%s\n", k, entries[k])
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s: %d prefixes, %d bytes", *out, len(keys), buf.Len())
}

func fetch(client *http.Client, url string, into map[string]string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")
	req.Header.Set("Accept", "text/csv,text/html;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	r := csv.NewReader(resp.Body)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	n := 0
	for i := 0; ; i++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return n, err
		}
		if i == 0 || len(rec) < 3 { // header
			continue
		}
		prefix := strings.ToUpper(strings.TrimSpace(rec[1]))
		vendor := clean(rec[2])
		if !isHex(prefix) || (len(prefix) != 6 && len(prefix) != 7 && len(prefix) != 9) || vendor == "" {
			continue
		}
		into[prefix] = vendor
		n++
	}
	return n, nil
}

func clean(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}
