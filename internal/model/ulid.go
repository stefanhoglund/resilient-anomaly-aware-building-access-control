package model

import (
	"crypto/rand"
	"fmt"
	"time"
)

// A ULID is a 128-bit identifier: a 48-bit big-endian millisecond
// timestamp followed by 80 bits of randomness, rendered as 26 characters
// of Crockford base32. Two useful properties for an event bus:
//
//   - ids are unique without any coordination between producers;
//   - ids sort lexicographically in (roughly) creation order, which
//     makes an event log readable and gives the bus a natural resume
//     cursor.
//
// We implement the small amount of encoding ourselves rather than take a
// dependency (CLAUDE.md: prefer the standard library).
//
// Reference: https://github.com/ulid/spec

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID returns a ULID string for the given instant. It panics only if
// the system CSPRNG fails, which a process cannot sensibly continue past.
func NewULID(t time.Time) string {
	ms := uint64(t.UnixMilli())
	if ms >= 1<<48 {
		// ~10889 AD; not reachable in practice.
		panic("model: ULID timestamp out of range")
	}

	var b [16]byte
	// 48-bit timestamp, big-endian, in the first 6 bytes.
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	// 80 bits of randomness in the remaining 10 bytes.
	if _, err := rand.Read(b[6:]); err != nil {
		panic(fmt.Sprintf("model: crypto/rand failed: %v", err))
	}

	return encodeBase32(b)
}

// NewID is the conventional way to mint an event id: a ULID stamped with
// the current wall-clock time.
func NewID() string { return NewULID(time.Now()) }

// encodeBase32 renders 128 bits as 26 Crockford base32 characters. The
// 128 bits are treated as a 130-bit number with two leading zero bits, so
// the first character is always one of 0-7.
func encodeBase32(b [16]byte) string {
	// Accumulate all 128 bits into two uint64 halves for simple shifting.
	hi := uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
	lo := uint64(b[8])<<56 | uint64(b[9])<<48 | uint64(b[10])<<40 | uint64(b[11])<<32 |
		uint64(b[12])<<24 | uint64(b[13])<<16 | uint64(b[14])<<8 | uint64(b[15])

	var out [26]byte
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&0x1f]
		// Shift the 128-bit value right by 5, carrying from hi into lo.
		lo = (lo >> 5) | (hi << 59)
		hi >>= 5
	}
	return string(out[:])
}

// ValidULID reports whether s is a syntactically valid ULID: 26 Crockford
// base32 characters with a first character in 0-7 (so the value fits 128
// bits).
func ValidULID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if decodeChar(s[i]) < 0 {
			return false
		}
	}
	return decodeChar(s[0]) < 8
}

// ULIDTime extracts the millisecond timestamp encoded in a ULID's first
// 10 characters (50 bits, of which the top 2 are always zero). It is
// handy for reading an event log. Returns false if s is not a valid ULID.
func ULIDTime(s string) (time.Time, bool) {
	if !ValidULID(s) {
		return time.Time{}, false
	}
	var ms uint64
	for i := 0; i < 10; i++ {
		ms = ms<<5 | uint64(decodeChar(s[i]))
	}
	return time.UnixMilli(int64(ms)).UTC(), true
}

func decodeChar(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'Z':
		// Crockford omits I, L, O, U.
		switch c {
		case 'I', 'L', 'O', 'U':
			return -1
		}
		for i := 10; i < len(crockford); i++ {
			if crockford[i] == c {
				return i
			}
		}
	}
	return -1
}
