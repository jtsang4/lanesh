// Package auth generates and verifies Lanesh credentials.
//
// Credentials are high-entropy random strings with a kind prefix. Only their
// SHA-256 hash is stored, and comparison happens on hashes in constant time.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// Credential kinds and their prefixes.
const (
	PrefixAdmin   = "lsa_"
	PrefixJoin    = "lsj_"
	PrefixMachine = "lsm_"
	PrefixAccess  = "lsk_"
	PrefixTicket  = "lst_"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// RandomString returns n characters from a base62 alphabet using crypto/rand.
func RandomString(n int) string {
	var b strings.Builder
	b.Grow(n)
	buf := make([]byte, 1)
	for b.Len() < n {
		if _, err := rand.Read(buf); err != nil {
			panic("auth: crypto/rand failed: " + err.Error())
		}
		// Rejection sampling keeps the distribution uniform.
		if int(buf[0]) >= 256-(256%len(alphabet)) {
			continue
		}
		b.WriteByte(alphabet[int(buf[0])%len(alphabet)])
	}
	return b.String()
}

// NewToken returns a new credential with the given prefix (about 238 bits of entropy).
func NewToken(prefix string) string {
	return prefix + RandomString(40)
}

// NewID returns a short random identifier with the given prefix.
func NewID(prefix string) string {
	return prefix + RandomString(16)
}

// Hash returns the hex SHA-256 of a credential, used for storage and lookup.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Equal compares two hashes in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// DisplayPrefix returns the identifying start of a credential that is safe to
// show in lists, e.g. "lsk_AbC1".
func DisplayPrefix(token string) string {
	if i := strings.IndexByte(token, '_'); i >= 0 && len(token) >= i+5 {
		return token[:i+5]
	}
	return ""
}

// HasPrefix reports whether token carries the given kind prefix.
func HasPrefix(token, prefix string) bool {
	return strings.HasPrefix(token, prefix)
}
