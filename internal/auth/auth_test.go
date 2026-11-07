package auth

import (
	"strings"
	"testing"
	"time"
)

func TestNewTokenIsPrefixedAndUnique(t *testing.T) {
	a, b := NewToken(PrefixAccess), NewToken(PrefixAccess)
	if !strings.HasPrefix(a, PrefixAccess) || len(a) != len(PrefixAccess)+40 {
		t.Fatalf("unexpected token %q", a)
	}
	if a == b {
		t.Fatal("tokens must be unique")
	}
	if DisplayPrefix(a) != a[:8] {
		t.Fatalf("DisplayPrefix = %q", DisplayPrefix(a))
	}
}

func TestHashEqual(t *testing.T) {
	tok := NewToken(PrefixJoin)
	if !Equal(Hash(tok), Hash(tok)) || Equal(Hash(tok), Hash(tok+"x")) {
		t.Fatal("hash comparison is wrong")
	}
}

func TestBriefingRoundTrip(t *testing.T) {
	b := NewBriefings([]byte("secret"))
	now := time.Unix(1_800_000_000, 0)
	tok := b.Issue("ln_1", "m_1", 42, now)

	got, err := b.Verify("ln_1", "m_1", tok)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IssuedAt.Equal(now) || got.SkillsTag != 42 {
		t.Fatalf("got %+v", got)
	}

	for name, c := range map[string]struct{ lane, machine, tok string }{
		"other machine": {"ln_1", "m_2", tok},
		"other lane":    {"ln_2", "m_1", tok},
		"tampered":      {"ln_1", "m_1", tok[:len(tok)-2] + "AA"},
		"garbage":       {"ln_1", "m_1", "brf_nope"},
		"no prefix":     {"ln_1", "m_1", "abc"},
	} {
		if _, err := b.Verify(c.lane, c.machine, c.tok); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := NewBriefings([]byte("other")).Verify("ln_1", "m_1", tok); err == nil {
		t.Error("other secret must not verify")
	}
}
