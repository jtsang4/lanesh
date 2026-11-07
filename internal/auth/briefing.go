package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

// BriefingPrefix marks briefing tokens returned by describe_machine.
const BriefingPrefix = "brf_"

// ErrInvalidBriefing is returned for briefing tokens that do not verify.
var ErrInvalidBriefing = errors.New("invalid briefing")

// Briefings issues and verifies stateless briefing tokens.
//
// A briefing token proves that the caller obtained it from describe_machine
// for a specific lane and machine. It embeds the issue time and a short hash
// of the machine's skill index so that callers can be told when the skills
// changed since they last looked.
type Briefings struct {
	secret []byte
}

// NewBriefings creates a verifier keyed by secret.
func NewBriefings(secret []byte) *Briefings {
	return &Briefings{secret: secret}
}

// Briefing is the verified content of a briefing token.
type Briefing struct {
	IssuedAt  time.Time
	SkillsTag uint32
}

// SkillsTag condenses a skill index hash into the 32-bit tag stored in briefings.
func SkillsTag(skillsHash string) uint32 {
	sum := sha256.Sum256([]byte(skillsHash))
	return binary.BigEndian.Uint32(sum[:4])
}

func (b *Briefings) mac(laneID, machineID string, payload []byte) []byte {
	m := hmac.New(sha256.New, b.secret)
	m.Write([]byte(laneID))
	m.Write([]byte{0})
	m.Write([]byte(machineID))
	m.Write([]byte{0})
	m.Write(payload)
	return m.Sum(nil)[:12]
}

// Issue returns a briefing token for laneID/machineID.
func (b *Briefings) Issue(laneID, machineID string, skillsTag uint32, now time.Time) string {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint64(payload[:8], uint64(now.Unix())) //nolint:gosec // Unix time is positive.
	binary.BigEndian.PutUint32(payload[8:], skillsTag)
	raw := make([]byte, 0, 24)
	raw = append(raw, payload...)
	raw = append(raw, b.mac(laneID, machineID, payload)...)
	return BriefingPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// Verify checks that token was issued for laneID/machineID.
func (b *Briefings) Verify(laneID, machineID, token string) (Briefing, error) {
	enc, ok := strings.CutPrefix(strings.TrimSpace(token), BriefingPrefix)
	if !ok {
		return Briefing{}, ErrInvalidBriefing
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || len(raw) != 24 {
		return Briefing{}, ErrInvalidBriefing
	}
	payload, sig := raw[:12], raw[12:]
	if !hmac.Equal(sig, b.mac(laneID, machineID, payload)) {
		return Briefing{}, ErrInvalidBriefing
	}
	return Briefing{
		IssuedAt:  time.Unix(int64(binary.BigEndian.Uint64(payload[:8])), 0), //nolint:gosec // Round-trips Issue.
		SkillsTag: binary.BigEndian.Uint32(payload[8:]),
	}, nil
}
