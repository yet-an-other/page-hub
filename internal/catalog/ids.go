package catalog

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewID returns a random UUIDv4 identity. Project, Publication, manifest
// revision, observation, and adoption operation identities are all random
// UUIDv4 values.
func NewID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		// crypto/rand failing means the OS entropy source is broken; there is
		// no safe way to continue issuing identities.
		panic(fmt.Sprintf("catalog: generate UUIDv4: %v", err))
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40 // version 4
	bytes[8] = (bytes[8] & 0x3f) | 0x80 // RFC 4122 variant
	hexed := hex.EncodeToString(bytes[:])
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
}
