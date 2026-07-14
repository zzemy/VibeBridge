package securestore

import (
	"crypto/sha256"
	"crypto/subtle"
)

func purposeDigest(purpose string) []byte {
	digest := sha256.Sum256([]byte("VibeBridge secure store purpose\x00" + purpose))
	return digest[:]
}

func equalPurpose(left []byte, right []byte) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare(left, right) == 1
}
