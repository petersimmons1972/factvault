package agentcomms

import (
	"crypto/rand"
	"fmt"
	"time"
)

// crockfordAlphabet is Crockford's base32 alphabet (0-9 then A-Z minus I,L,O,U).
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID returns a 26-character ULID for the given time, using crypto/rand
// for the 80-bit randomness component. Layout per the ULID spec:
//
//	48-bit timestamp (ms since epoch)  -> 10 base32 chars
//	80-bit randomness                  -> 16 base32 chars
func NewULID(t time.Time) (string, error) {
	ms := uint64(t.UnixMilli())
	var entropy [10]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", fmt.Errorf("ulid entropy: %w", err)
	}

	// Build 16 bytes: 6 bytes timestamp || 10 bytes entropy.
	var raw [16]byte
	raw[0] = byte((ms >> 40) & 0xFF)
	raw[1] = byte((ms >> 32) & 0xFF)
	raw[2] = byte((ms >> 24) & 0xFF)
	raw[3] = byte((ms >> 16) & 0xFF)
	raw[4] = byte((ms >> 8) & 0xFF)
	raw[5] = byte(ms & 0xFF)
	copy(raw[6:], entropy[:])

	return encodeULID(raw), nil
}

// encodeULID encodes the 16-byte ULID payload as 26 Crockford base32 characters.
// The first character carries only 3 bits of the 48-bit timestamp; the high 2
// bits are always zero, which is why valid ULIDs start with 0-7 (matching the
// 0–7ZZZZ... range that fits in 48 bits).
func encodeULID(raw [16]byte) string {
	var out [26]byte
	// Timestamp (10 chars from 6 bytes / 48 bits, MSB-first).
	out[0] = crockfordAlphabet[(raw[0]&224)>>5]
	out[1] = crockfordAlphabet[raw[0]&31]
	out[2] = crockfordAlphabet[(raw[1]&248)>>3]
	out[3] = crockfordAlphabet[((raw[1]&7)<<2)|((raw[2]&192)>>6)]
	out[4] = crockfordAlphabet[(raw[2]&62)>>1]
	out[5] = crockfordAlphabet[((raw[2]&1)<<4)|((raw[3]&240)>>4)]
	out[6] = crockfordAlphabet[((raw[3]&15)<<1)|((raw[4]&128)>>7)]
	out[7] = crockfordAlphabet[(raw[4]&124)>>2]
	out[8] = crockfordAlphabet[((raw[4]&3)<<3)|((raw[5]&224)>>5)]
	out[9] = crockfordAlphabet[raw[5]&31]
	// Entropy (16 chars from 10 bytes / 80 bits).
	out[10] = crockfordAlphabet[(raw[6]&248)>>3]
	out[11] = crockfordAlphabet[((raw[6]&7)<<2)|((raw[7]&192)>>6)]
	out[12] = crockfordAlphabet[(raw[7]&62)>>1]
	out[13] = crockfordAlphabet[((raw[7]&1)<<4)|((raw[8]&240)>>4)]
	out[14] = crockfordAlphabet[((raw[8]&15)<<1)|((raw[9]&128)>>7)]
	out[15] = crockfordAlphabet[(raw[9]&124)>>2]
	out[16] = crockfordAlphabet[((raw[9]&3)<<3)|((raw[10]&224)>>5)]
	out[17] = crockfordAlphabet[raw[10]&31]
	out[18] = crockfordAlphabet[(raw[11]&248)>>3]
	out[19] = crockfordAlphabet[((raw[11]&7)<<2)|((raw[12]&192)>>6)]
	out[20] = crockfordAlphabet[(raw[12]&62)>>1]
	out[21] = crockfordAlphabet[((raw[12]&1)<<4)|((raw[13]&240)>>4)]
	out[22] = crockfordAlphabet[((raw[13]&15)<<1)|((raw[14]&128)>>7)]
	out[23] = crockfordAlphabet[(raw[14]&124)>>2]
	out[24] = crockfordAlphabet[((raw[14]&3)<<3)|((raw[15]&224)>>5)]
	out[25] = crockfordAlphabet[raw[15]&31]
	return string(out[:])
}
