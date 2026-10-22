package store

import (
	"io"
	"os"
	"path/filepath"
)

func pathParent(p string) string {
	a, e := filepath.EvalSymlinks(p)
	if e != nil {
		return filepath.Dir(p)
	}
	a, _ = filepath.Abs(a)
	return filepath.Dir(a)
}
func publishPromotion(stage, served, name string) error {
	resolved, e := filepath.EvalSymlinks(served)
	if e != nil {
		return e
	}
	resolved, e = filepath.Abs(resolved)
	if e != nil {
		return e
	}
	// Pin the destination parent through its own Root before linking; never permit
	// a relative destination symlink to escape the served tree.
	dest, e := os.OpenRoot(resolved)
	if e != nil {
		return e
	}
	defer dest.Close()
	parent, e := dest.OpenRoot(filepath.Dir(filepath.FromSlash(name)))
	if e != nil {
		return e
	}
	defer parent.Close()
	// os.Root.Link cannot link across two roots. Move the staging file into a
	// private directory in the pinned destination parent, then link the final name.
	temp := ".ztg-promote-" + randomID()
	if e = parent.Mkdir(temp, 0700); e != nil {
		return e
	}
	defer parent.RemoveAll(temp)
	// The directory must never be readable by TFTP, even during publication.
	f, e := parent.OpenFile(temp+"/payload", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	in, e := os.Open(filepath.Join(stage, "payload"))
	if e != nil {
		f.Close()
		return e
	}
	_, e = io.Copy(f, in)
	in.Close()
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return parent.Link(temp+"/payload", filepath.Base(filepath.FromSlash(name)))
}
