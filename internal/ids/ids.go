// Package ids generates the opaque, time-sortable identifiers every entity carries.
//
// An id is a prefix plus 26 characters of Crockford base32 over 16 bytes: a 6-byte
// big-endian millisecond timestamp followed by 10 random bytes. That layout is ULID's, and
// because the alphabet is in ASCII order and the length is fixed, the encoded strings sort
// chronologically — so `ORDER BY id` is a time order and it is possible to tell by eye
// which of two ids is older.
//
// Crockford's alphabet omits I, L, O and U, so an id cannot be misread between similar
// glyphs or accidentally spell something.
//
// Ids are opaque and never parsed back. The prefix is for the human reading a log line.
package ids

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"strings"
	"time"
)

// Prefixes make an id self-describing in a log line.
const (
	Principal = "p_"
	Invite    = "i_"
	Tag       = "t_"
	Reminder  = "r_"
	Nudge     = "n_"
	Device    = "d_"
	Job       = "j_"
)

// crockford is base32 without I, L, O or U, in ASCII order so encoded strings sort the way
// the bytes do.
var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// Length is the character count after the prefix: 16 bytes in base32.
const Length = 26

// New mints an identifier with the given prefix.
func New(prefix string) string { return newAt(prefix, time.Now()) }

func newAt(prefix string, t time.Time) string {
	var b [16]byte
	// Six bytes of big-endian milliseconds, which is ULID's field and runs to the year
	// 10889. Written as the low six bytes of a uint64 because there is no PutUint48.
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(t.UTC().UnixMilli()))
	copy(b[:6], ms[2:])
	// crypto/rand.Read never returns an error; it panics if the system source fails, which
	// is the right outcome for a process that is about to mint identifiers.
	rand.Read(b[6:])
	return prefix + crockford.EncodeToString(b[:])
}

// Valid reports whether s is an identifier of the given kind.
//
// It checks shape, never existence. A handler uses it to refuse a malformed id before it
// reaches a query, so a typo comes back as a 400 rather than an empty result set that
// looks like a 404.
func Valid(prefix, s string) bool {
	if !strings.HasPrefix(s, prefix) || len(s) != len(prefix)+Length {
		return false
	}
	_, err := crockford.DecodeString(s[len(prefix):])
	return err == nil
}
