package auth

import (
	"strings"
	"testing"
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

func TestBriefingTokens(t *testing.T) {
	b := NewBriefings([]byte("secret"))
	tok := b.Token("ln_1", "m_1", "build-box", "h1")
	if !strings.HasPrefix(tok, "build-box:") || len(tok) != len("build-box:")+8 {
		t.Fatalf("token %q", tok)
	}
	if b.Token("ln_1", "m_1", "build-box", "h1") != tok {
		t.Fatal("tokens must be stable for the same state")
	}
	if !b.Valid("ln_1", "m_1", "h1", tok) || !b.Valid("ln_1", "m_1", "h1", "renamed:"+tok[len("build-box:"):]) {
		t.Fatal("token must validate, whatever the name part says")
	}
	for name, c := range map[string]struct{ lane, machine, hash, tok string }{
		"skills changed": {"ln_1", "m_1", "h2", tok},
		"other machine":  {"ln_1", "m_2", "h1", tok},
		"other lane":     {"ln_2", "m_1", "h1", tok},
		"no code":        {"ln_1", "m_1", "h1", "build-box"},
		"empty":          {"ln_1", "m_1", "h1", ""},
	} {
		if b.Valid(c.lane, c.machine, c.hash, c.tok) {
			t.Errorf("%s: must not validate", name)
		}
	}
	if NewBriefings([]byte("other")).Valid("ln_1", "m_1", "h1", tok) {
		t.Error("other secret must not validate")
	}
}
