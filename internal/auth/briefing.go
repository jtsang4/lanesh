package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"strings"
)

// Briefings issues and checks briefing tokens.
//
// A briefing token shows that a caller has seen describe_machine's briefing
// for a machine in its current state. It has the form "<machine-name>:<code>",
// where the code is an HMAC of the lane, the machine ID and the machine's
// skill index hash. Tokens are therefore short, stable while the machine's
// skills stay the same, and change as soon as they do. The name part only
// makes tokens easy for a model to keep apart; it is not checked, so renaming
// a machine does not invalidate tokens.
//
// Briefing tokens are not credentials (access keys are); they make sure an
// agent has looked at a machine before acting on it.
type Briefings struct {
	secret []byte
}

// NewBriefings creates a token issuer keyed by secret.
func NewBriefings(secret []byte) *Briefings {
	return &Briefings{secret: secret}
}

var codeEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func (b *Briefings) code(laneID, machineID, skillsHash string) string {
	m := hmac.New(sha256.New, b.secret)
	m.Write([]byte(laneID))
	m.Write([]byte{0})
	m.Write([]byte(machineID))
	m.Write([]byte{0})
	m.Write([]byte(skillsHash))
	return codeEncoding.EncodeToString(m.Sum(nil)[:5])
}

// Token returns the briefing token for a machine in its current state.
func (b *Briefings) Token(laneID, machineID, machineName, skillsHash string) string {
	return machineName + ":" + b.code(laneID, machineID, skillsHash)
}

// Valid reports whether token matches the machine's current state.
func (b *Briefings) Valid(laneID, machineID, skillsHash, token string) bool {
	token = strings.TrimSpace(token)
	i := strings.LastIndexByte(token, ':')
	if i < 0 {
		return false
	}
	return hmac.Equal([]byte(token[i+1:]), []byte(b.code(laneID, machineID, skillsHash)))
}
