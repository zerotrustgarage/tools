package auth

// protectBlock is CRYPTPROTECTMEMORY_BLOCK_SIZE: CryptProtectMemory works on
// whole 16-byte blocks.
const protectBlock = 16

// protectPassword copies p into a buffer padded to whole blocks and encrypts
// it in place for this process (design 6.5 step 7: the credential is kept
// encrypted between the connections that need it). It returns the blob and
// the password length. p is not modified; the caller wipes it.
func protectPassword(p []byte) ([]byte, int, error) {
	size := (len(p)/protectBlock + 1) * protectBlock
	blob := make([]byte, size)
	copy(blob, p)
	if err := protectMemory(blob); err != nil {
		wipe(blob)
		return nil, 0, err
	}
	return blob, len(p), nil
}

// revealPassword decrypts a copy of blob and returns the password; the caller
// wipes the result as soon as the bind has used it. The blob is unchanged.
func revealPassword(blob []byte, n int) ([]byte, error) {
	if n < 0 || n > len(blob) || len(blob)%protectBlock != 0 {
		return nil, ErrSessionClosed
	}
	plain := append([]byte(nil), blob...)
	if err := unprotectMemory(plain); err != nil {
		wipe(plain)
		return nil, err
	}
	wipe(plain[n:])
	return plain[:n:n], nil
}
