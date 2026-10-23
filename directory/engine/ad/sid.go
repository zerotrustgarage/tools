package ad

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SID layout follows Microsoft's [MS-DTYP] SID specification: a revision,
// subauthority count, a big-endian 48-bit authority, then little-endian RIDs.
func validSID(sid []byte) bool {
	return len(sid) >= 12 && sid[0] == 1 && sid[1] > 0 && sid[1] <= 15 && len(sid) == 8+4*int(sid[1])
}

func primaryGroupSID(userSID []byte, rid uint32) ([]byte, error) {
	if !validSID(userSID) || userSID[1] == 0 {
		return nil, errors.New("user objectSid is malformed")
	}
	result := append([]byte(nil), userSID...)
	binary.LittleEndian.PutUint32(result[len(result)-4:], rid)
	return result, nil
}

func sidFilterValue(sid []byte) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	for _, v := range sid {
		b.WriteByte('\\')
		b.WriteByte(hex[v>>4])
		b.WriteByte(hex[v&15])
	}
	return b.String()
}

func sidText(sid []byte) string {
	if !validSID(sid) {
		return "invalid"
	}
	authority := uint64(0)
	for _, v := range sid[2:8] {
		authority = authority<<8 | uint64(v)
	}
	parts := []string{fmt.Sprintf("S-%d-%d", sid[0], authority)}
	for i := 0; i < int(sid[1]); i++ {
		parts = append(parts, strconv.FormatUint(uint64(binary.LittleEndian.Uint32(sid[8+i*4:])), 10))
	}
	return strings.Join(parts, "-")
}
