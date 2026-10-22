// Package store applies a portable name policy before runtime-enforced containment.
package store

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

func Name(name string) (string, error) {
	if !utf8.ValidString(name) || len(name) > 1024 {
		return "", errors.New("name-policy")
	}
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "//") {
		return "", errors.New("name-policy")
	}
	name = strings.TrimPrefix(name, "/")
	for _, c := range name {
		if unicode.IsControl(c) || c == ':' {
			return "", errors.New("name-policy")
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(strings.ToLower(part), ".ztg-promote-") || part == "" || part == "." || part == ".." || len(part) > 255 || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return "", errors.New("name-policy")
		}
		base := strings.TrimRight(strings.ToUpper(strings.SplitN(part, ".", 2)[0]), " ")
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" {
			return "", errors.New("name-policy")
		}
		if (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && len([]rune(base)) == 4 && strings.ContainsRune("123456789¹²³", []rune(base)[3]) {
			return "", errors.New("name-policy")
		}
	}
	return name, nil
}

type Store struct {
	root         *os.Root
	FoldCase     bool
	approved     map[string]string
	snapshotDir  string
	snapshots    *os.Root
	snapshotLock chan struct{}
	cached       map[string]string
}

func Open(path string, foldCase bool) (*Store, error) {
	r, e := os.OpenRoot(path)
	if e != nil {
		return nil, e
	}
	return &Store{root: r, FoldCase: foldCase}, nil
}
func (s *Store) Close() error {
	if s.snapshots != nil {
		s.snapshots.Close()
		os.RemoveAll(s.snapshotDir)
	}
	return s.root.Close()
}

func (s *Store) Read(name string) (*os.File, error) {
	if s.approved != nil {
		return s.approvedRead(context.Background(), name)
	}
	return s.readOriginal(name)
}
func (s *Store) ReadContext(ctx context.Context, name string) (*os.File, error) {
	if s.approved != nil {
		return s.approvedRead(ctx, name)
	}
	return s.readOriginal(name)
}
func (s *Store) readOriginal(name string) (*os.File, error) {
	n, e := Name(name)
	if e != nil {
		return nil, e
	}
	if s.FoldCase {
		parts := strings.Split(n, "/")
		dir := "."
		for i, part := range parts {
			f, e := s.root.OpenFile(dir, os.O_RDONLY|nonblock, 0)
			if e != nil {
				return nil, e
			}
			match := ""
			for {
				entries, err := f.ReadDir(256)
				if err != nil && err != io.EOF {
					f.Close()
					return nil, err
				}
				for _, entry := range entries {
					if strings.EqualFold(entry.Name(), part) {
						if match != "" {
							f.Close()
							return nil, errors.New("ambiguous name")
						}
						match = entry.Name()
					}
				}
				if err == io.EOF {
					break
				}
			}
			f.Close()
			if match == "" {
				return nil, os.ErrNotExist
			}
			parts[i] = match
			dir = strings.Join(parts[:i+1], "/")
		}
		n = strings.Join(parts, "/")
	}
	// Stat before Open avoids blocking on a FIFO. Open and Stat again to reject a
	// changed target; os.Root enforces containment on both operations.
	info, e := s.root.Stat(n)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, e := s.root.OpenFile(n, os.O_RDONLY|nonblock, 0)
	if e != nil {
		return nil, e
	}
	info, e = f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("not a regular file")
	}
	return f, nil
}
