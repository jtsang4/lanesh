package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jtsang4/lanesh/internal/auth"
	"github.com/jtsang4/lanesh/internal/protocol"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestLanesAndKeys(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	l, err := s.CreateLane(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	k, token, err := s.CreateKey(ctx, l.ID, KindAccess, "laptop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.HasPrefix(token, auth.PrefixAccess) || k.Prefix != token[:8] {
		t.Fatalf("token %q key %+v", token, k)
	}
	if _, err := s.ValidateKey(ctx, l.ID, KindAccess, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateKey(ctx, l.ID, KindJoin, token); !errors.Is(err, ErrNotFound) {
		t.Fatal("kind must match")
	}
	if _, err := s.ValidateKey(ctx, "ln_other", KindAccess, token); !errors.Is(err, ErrNotFound) {
		t.Fatal("lane must match")
	}
	if err := s.RevokeKey(ctx, l.ID, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateKey(ctx, l.ID, KindAccess, token); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked key must not validate")
	}

	past := time.Now().Add(-time.Hour)
	_, expired, _ := s.CreateKey(ctx, l.ID, KindJoin, "old", &past)
	if _, err := s.ValidateKey(ctx, l.ID, KindJoin, expired); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired key must not validate")
	}

	keys, _ := s.ListKeys(ctx, l.ID)
	if len(keys) != 2 {
		t.Fatalf("keys = %+v", keys)
	}
	if err := s.DeleteLane(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.ListKeys(ctx, l.ID); len(keys) != 0 {
		t.Fatal("keys must cascade")
	}
}

func TestMemberships(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	l, _ := s.CreateLane(ctx, "home")
	other, _ := s.CreateLane(ctx, "other")
	pol := protocol.Policy{ExposeSkills: true, AllowExec: true}
	m1 := Machine{ID: "m1", OS: "linux", Arch: "amd64", Hostname: "box", Version: "1.0.0"}
	m2 := Machine{ID: "m2", OS: "linux", Arch: "amd64", Hostname: "box", Version: "1.0.0"}

	a, err := s.Join(ctx, l.ID, m1, "box", auth.Hash("c1"), "", pol)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Join(ctx, l.ID, m2, "box", auth.Hash("c2"), "", pol)
	if a.DisplayName != "box" || b.DisplayName != "box-2" {
		t.Fatalf("names %q %q", a.DisplayName, b.DisplayName)
	}

	// An active membership cannot be taken over with a join key alone.
	if _, err := s.Join(ctx, l.ID, m1, "evil", auth.Hash("x"), "", pol); !errors.Is(err, ErrConflict) {
		t.Fatalf("takeover without proof: %v", err)
	}
	if _, err := s.Join(ctx, l.ID, m1, "evil", auth.Hash("x"), auth.Hash("wrong"), pol); !errors.Is(err, ErrConflict) {
		t.Fatalf("takeover with wrong proof: %v", err)
	}
	// With the current credential, re-joining rotates it and keeps the name.
	again, err := s.Join(ctx, l.ID, m1, "renamed", auth.Hash("c3"), auth.Hash("c1"), pol)
	if err != nil || again.DisplayName != "box" {
		t.Fatalf("rejoin: %+v %v", again, err)
	}
	if _, err := s.MembershipByCredential(ctx, l.ID, auth.Hash("c1")); !errors.Is(err, ErrNotFound) {
		t.Fatal("old credential must be replaced")
	}
	if m, err := s.MembershipByCredential(ctx, l.ID, auth.Hash("c3")); err != nil || m.Machine.ID != "m1" {
		t.Fatalf("m = %+v err = %v", m, err)
	}

	// A removed machine may join again with a join key.
	if err := s.RevokeMembership(ctx, l.ID, "m2"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListMemberships(ctx, l.ID); len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	if _, err := s.Join(ctx, l.ID, m2, "box", auth.Hash("c4"), "", pol); err != nil {
		t.Fatalf("rejoin after removal: %v", err)
	}

	// Details and skills are stored per lane.
	if _, err := s.Join(ctx, other.ID, Machine{ID: "m1", OS: "plan9"}, "x", auth.Hash("o1"), "", pol); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetMembership(ctx, l.ID, "m1"); got.Machine.OS != "linux" {
		t.Fatalf("other lane changed details: %+v", got)
	}
	_ = s.SaveSkills(ctx, l.ID, "m1", protocol.SkillIndex{Skills: []protocol.Skill{{Name: "a"}}, Hash: "h"})
	_ = s.SaveSkills(ctx, other.ID, "m1", protocol.SkillIndex{Hash: "other"})
	got, _, err := s.Skills(ctx, l.ID, "m1")
	if err != nil || got.Hash != "h" || len(got.Skills) != 1 {
		t.Fatalf("skills = %+v err = %v", got, err)
	}
}

func TestSessionsAuditSecret(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	tok, _ := s.CreateSession(ctx, time.Hour)
	if !s.ValidSession(ctx, tok) || s.ValidSession(ctx, "nope") {
		t.Fatal("session validation is wrong")
	}
	_ = s.DeleteSession(ctx, tok)
	if s.ValidSession(ctx, tok) {
		t.Fatal("deleted session must be invalid")
	}

	for i := range 3 {
		_ = s.AddAudit(ctx, AuditEntry{LaneID: "l", Actor: "k", Action: "exec", MachineID: "m", Detail: map[string]any{"i": i}, Result: "ok"})
	}
	page, _ := s.ListAudit(ctx, AuditFilter{LaneID: "l", Limit: 2})
	if len(page) != 2 || page[0].Detail["i"].(float64) != 2 {
		t.Fatalf("page = %+v", page)
	}
	next, _ := s.ListAudit(ctx, AuditFilter{LaneID: "l", Before: page[1].ID})
	if len(next) != 1 {
		t.Fatalf("next = %+v", next)
	}

	a, _ := s.Secret(ctx, "briefing")
	b, _ := s.Secret(ctx, "briefing")
	if len(a) != 32 || string(a) != string(b) {
		t.Fatal("secret must persist")
	}
}
