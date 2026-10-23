package run

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/zerotrustgarage/tools/copy/internal/parse"
)

// Read supplemental diagnostics and hash the entire log in one streaming pass.
// Correctness still comes from the bounded native summary and engine exit code.
func readNative(name string) (summary parse.Summary, hash string, diagnostics parse.Diagnostics, err error) {
	f, err := os.Open(name)
	if err != nil {
		return
	}
	defer f.Close()
	h := sha256.New()
	var diagnosticErr error
	diagnostics, diagnosticErr = parse.NativeDiagnostics(io.TeeReader(f, h))
	if diagnosticErr != nil {
		diagnostics.Unavailable = diagnosticErr.Error()
	}
	// Finish hashing even when diagnostic decoding stops before EOF.
	if _, err = io.Copy(h, f); err != nil {
		return
	}
	hash = hex.EncodeToString(h.Sum(nil))
	info, err := f.Stat()
	if err != nil {
		return
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return
	}
	bom := [2]byte{}
	if _, err = io.ReadFull(f, bom[:]); err != nil || bom != [2]byte{0xff, 0xfe} || info.Size()%2 != 0 {
		err = errors.New("native log is not complete UTF-16LE")
		return
	}
	offset := int64(0)
	if info.Size() > 1<<20 {
		offset = (info.Size() - 1<<20) &^ 1
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+2))
	if err != nil {
		return
	}
	if offset > 0 {
		found := false
		for i := 0; i+1 < len(data); i += 2 {
			if data[i] == 10 && data[i+1] == 0 {
				data = append([]byte{0xff, 0xfe}, data[i+2:]...)
				found = true
				break
			}
		}
		if !found {
			err = errors.New("native log footer has no complete line")
			return
		}
	}
	summary, err = parse.NativeSummary(data)
	return
}
