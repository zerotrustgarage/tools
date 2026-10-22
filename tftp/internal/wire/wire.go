// Package wire implements the RFC 1350 and RFC 2347 packet formats.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	RRQ uint16 = 1 + iota
	WRQ
	DATA
	ACK
	ERROR
	OACK
)

const MaxBlock = 65464

type Packet struct {
	Op          uint16
	Name, Mode  string
	Options     map[string]string
	Block, Code uint16
	Data        []byte
	Message     string
}

func fields(b []byte) ([]string, error) {
	if len(b) == 0 || b[len(b)-1] != 0 {
		return nil, errors.New("missing terminator")
	}
	f := strings.Split(string(b[:len(b)-1]), "\x00")
	for _, s := range f {
		if s == "" {
			return nil, errors.New("empty field")
		}
	}
	return f, nil
}

// ParseOptions rejects duplicate and excessive fields before allocating a map.
func ParseOptions(f []string) (map[string]string, error) {
	if len(f)%2 != 0 || len(f) > 64 {
		return nil, errors.New("invalid options")
	}
	m := make(map[string]string, len(f)/2)
	for i := 0; i < len(f); i += 2 {
		k := strings.ToLower(f[i])
		if k == "" || f[i+1] == "" || len(k) > 64 || len(f[i+1]) > 64 {
			return nil, errors.New("invalid option field")
		}
		if _, ok := m[k]; ok {
			return nil, errors.New("duplicate option")
		}
		m[k] = f[i+1]
	}
	return m, nil
}

func Decode(b []byte) (Packet, error) {
	p := Packet{}
	if len(b) < 2 || len(b) > MaxBlock+4 {
		return p, errors.New("invalid packet length")
	}
	p.Op = binary.BigEndian.Uint16(b)
	switch p.Op {
	case RRQ, WRQ:
		if len(b) > 2048 {
			return p, errors.New("request too long")
		}
		f, e := fields(b[2:])
		if e != nil || len(f) < 2 {
			return p, errors.New("invalid request")
		}
		p.Name, p.Mode = f[0], strings.ToLower(f[1])
		if p.Mode != "octet" && p.Mode != "netascii" {
			return p, errors.New("unsupported mode")
		}
		p.Options, e = ParseOptions(f[2:])
		return p, e
	case DATA:
		if len(b) < 4 {
			return p, errors.New("short DATA")
		}
		p.Block = binary.BigEndian.Uint16(b[2:])
		p.Data = b[4:]
	case ACK:
		if len(b) != 4 {
			return p, errors.New("invalid ACK")
		}
		p.Block = binary.BigEndian.Uint16(b[2:])
	case ERROR:
		if len(b) < 5 || b[len(b)-1] != 0 || strings.ContainsRune(string(b[4:len(b)-1]), 0) {
			return p, errors.New("invalid ERROR")
		}
		p.Code = binary.BigEndian.Uint16(b[2:])
		p.Message = string(b[4 : len(b)-1])
	case OACK:
		if len(b) > 2048 {
			return p, errors.New("OACK too long")
		}
		f, e := fields(b[2:])
		if e != nil {
			return p, e
		}
		p.Options, e = ParseOptions(f)
		return p, e
	default:
		return p, errors.New("unknown opcode")
	}
	return p, nil
}

func Encode(p Packet) []byte {
	b := binary.BigEndian.AppendUint16(nil, p.Op)
	field := func(s string) { b = append(b, s...); b = append(b, 0) }
	switch p.Op {
	case RRQ, WRQ:
		field(p.Name)
		field(p.Mode)
	case DATA:
		b = binary.BigEndian.AppendUint16(b, p.Block)
		return append(b, p.Data...)
	case ACK:
		return binary.BigEndian.AppendUint16(b, p.Block)
	case ERROR:
		b = binary.BigEndian.AppendUint16(b, p.Code)
		field(p.Message)
		return b
	}
	// Stable order makes negotiation, audit records and packet tests reproducible.
	keys := make([]string, 0, len(p.Options))
	for k := range p.Options {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		field(k)
		field(p.Options[k])
	}
	return b
}

type Params struct{ BlockSize, WindowSize, Timeout int }

func Defaults() Params { return Params{512, 1, 3} }

func Number(s string, min, max uint64) (uint64, error) {
	if s == "" {
		return 0, errors.New("empty number")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid number")
		}
	}
	n, e := strconv.ParseUint(s, 10, 64)
	if e != nil || n < min || n > max {
		return 0, errors.New("number out of range")
	}
	return n, nil
}

func Negotiate(opts map[string]string, maxBlock, maxWindow int, size int64, read bool) (Params, map[string]string, error) {
	p := Defaults()
	out := map[string]string{}
	for k, v := range opts {
		var min, max uint64
		switch k {
		case "blksize":
			min, max = 8, MaxBlock
		case "windowsize":
			min, max = 1, 65535
		case "timeout":
			min, max = 1, 255
		case "tsize":
			min, max = 0, 1<<63-1
		default:
			continue
		}
		n, e := Number(v, min, max)
		if e != nil {
			return p, nil, fmt.Errorf("invalid %s", k)
		}
		switch k {
		case "blksize":
			p.BlockSize = minInt(int(n), maxBlock)
			n = uint64(p.BlockSize)
		case "windowsize":
			p.WindowSize = minInt(int(n), maxWindow)
			n = uint64(p.WindowSize)
		case "timeout":
			p.Timeout = int(n)
		case "tsize":
			if read {
				if n != 0 {
					return p, nil, errors.New("RRQ tsize must be zero")
				}
				n = uint64(size)
			}
		}
		out[k] = strconv.FormatUint(n, 10)
	}
	return p, out, nil
}

// AcceptOACK forbids unsolicited options, increases of offered limits, and timeout changes.
func AcceptOACK(offered, got map[string]string) (Params, error) {
	p := Defaults()
	if len(got) == 0 {
		return p, errors.New("empty OACK")
	}
	for k, v := range got {
		o, ok := offered[k]
		if !ok {
			return p, errors.New("unsolicited option")
		}
		n, e := Number(v, 0, 1<<63-1)
		if e != nil {
			return p, e
		}
		limit, e := Number(o, 0, 1<<63-1)
		if e != nil {
			return p, e
		}
		switch k {
		case "blksize":
			if n < 8 || n > MaxBlock || n > limit {
				return p, errors.New("invalid blksize")
			}
			p.BlockSize = int(n)
		case "windowsize":
			if n < 1 || n > 64 || n > limit {
				return p, errors.New("invalid windowsize")
			}
			p.WindowSize = int(n)
		case "timeout":
			if n < 1 || n > 255 || n != limit {
				return p, errors.New("invalid timeout")
			}
			p.Timeout = int(n)
		case "tsize":
			if limit != 0 && n != limit {
				return p, errors.New("invalid tsize")
			}
		default:
			return p, errors.New("unsupported option")
		}
	}
	return p, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
