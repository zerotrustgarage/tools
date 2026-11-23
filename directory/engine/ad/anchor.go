package ad

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
)

// ImmutableID converts GUID text ("6f9619ff-8b86-d011-b42d-00c04fc964ff",
// optionally in braces), as Get returns objectGUID and mS-DS-ConsistencyGuid,
// to the value Microsoft Entra ID holds in onPremisesImmutableId: the Base64 of
// the GUID's 16 bytes in Microsoft's layout, whose first three fields are
// little-endian (MS-DTYP 2.3.4). It is the inverse of guidText.
func ImmutableID(guidText string) (string, error) {
	s := strings.TrimSpace(guidText)
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		s = s[1 : len(s)-1]
	}
	parts := strings.Split(s, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		return "", errors.New("not a GUID in the form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")
	}
	raw, err := hex.DecodeString(strings.Join(parts, ""))
	if err != nil {
		return "", errors.New("not a GUID: it contains characters that are not hexadecimal")
	}
	b := make([]byte, 16)
	binary.LittleEndian.PutUint32(b[0:4], binary.BigEndian.Uint32(raw[0:4]))
	binary.LittleEndian.PutUint16(b[4:6], binary.BigEndian.Uint16(raw[4:6]))
	binary.LittleEndian.PutUint16(b[6:8], binary.BigEndian.Uint16(raw[6:8]))
	copy(b[8:], raw[8:])
	return base64.StdEncoding.EncodeToString(b), nil
}
