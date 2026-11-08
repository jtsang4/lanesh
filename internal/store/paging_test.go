package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAuditQueriesUseIndexes guards the audit log's paging: newest-first pages
// with or without a filter must walk an index instead of sorting every row.
func TestAuditQueriesUseIndexes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	for _, f := range []AuditFilter{
		{LaneID: "l"},
		{LaneID: "l", Before: 100},
		{LaneID: "l", MachineID: "m"},
		{LaneID: "l", Action: "exec", Before: 100},
		{LaneID: "l", Actor: "key_1"},
	} {
		q, args := auditQuery(f)
		rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		_ = rows.Close()
		joined := strings.Join(plan, "; ")
		if strings.Contains(joined, "TEMP B-TREE") || !strings.Contains(joined, "USING INDEX audit_lane") {
			t.Errorf("%+v: plan %q", f, joined)
		}
	}
}

func TestQueryKeysPages(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	l, err := s.CreateLane(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	names := func(keys []Key) string {
		var out []string
		for _, k := range keys {
			out = append(out, k.Name)
		}
		return strings.Join(out, ",")
	}
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	var ids []string
	for i := range 7 {
		clock = clock.Add(time.Minute)
		var exp *time.Time
		if i == 1 {
			e := clock.Add(time.Second) // expires right away
			exp = &e
		}
		k, _, err := s.CreateKey(ctx, l.ID, KindAccess, fmt.Sprintf("a%d", i), exp)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, k.ID)
	}
	if _, _, err := s.CreateKey(ctx, l.ID, KindJoin, "j", nil); err != nil {
		t.Fatal(err)
	}
	// Keys created within the same second keep their creation order.
	for _, name := range []string{"b0", "b1", "b2"} {
		if _, _, err := s.CreateKey(ctx, l.ID, KindJoin, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	if page, _, _ := s.QueryKeys(ctx, KeyFilter{LaneID: l.ID, Kind: KindJoin}); names(page) != "b2,b1,b0,j" {
		t.Fatalf("same-second order: %s", names(page))
	}
	if err := s.RevokeKey(ctx, l.ID, ids[3]); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)

	// Active keys first, newest first; then revoked or expired ones.
	page, total, err := s.QueryKeys(ctx, KeyFilter{LaneID: l.ID, Kind: KindAccess, Limit: 3})
	if err != nil || total != 7 || names(page) != "a6,a5,a4" {
		t.Fatalf("page 1: %s total=%d %v", names(page), total, err)
	}
	page, _, _ = s.QueryKeys(ctx, KeyFilter{LaneID: l.ID, Kind: KindAccess, Limit: 3, Offset: 3})
	if names(page) != "a2,a0,a3" {
		t.Fatalf("page 2: %s", names(page))
	}
	page, total, _ = s.QueryKeys(ctx, KeyFilter{LaneID: l.ID, Kind: KindAccess, Status: KeyInactive})
	if total != 2 || names(page) != "a3,a1" {
		t.Fatalf("inactive: %s total=%d", names(page), total)
	}
	if _, total, _ = s.QueryKeys(ctx, KeyFilter{LaneID: l.ID, Status: KeyActive, Limit: 1}); total != 9 {
		t.Fatalf("active total = %d", total)
	}
	got, err := s.KeyNames(ctx, l.ID, []string{ids[0], ids[3], "key_missing"})
	if err != nil || got[ids[0]] != "a0" || got[ids[3]] != "a3" || len(got) != 2 {
		t.Fatalf("key names = %v %v", got, err)
	}
}

// TestMigrateFromVersion1 opens a database created by the first schema and
// checks that it is upgraded in place with its data.
func TestMigrateFromVersion1(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddAudit(ctx, AuditEntry{LaneID: "l", Actor: "admin", Action: "lane.create", Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	// Roll the schema back to version 1, as a database from v0.1.x would be.
	if _, err := s.db.ExecContext(ctx, `DROP INDEX audit_lane; DROP INDEX audit_lane_machine; DROP INDEX audit_lane_action;
		DROP INDEX audit_lane_actor; DROP INDEX audit_ts; DROP INDEX keys_lane;
		CREATE INDEX audit_lane_ts ON audit_log(lane_id, ts); CREATE INDEX keys_lane ON keys(lane_id); PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version int
	_ = s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	var idx string
	err = s.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'audit_lane_actor'").Scan(&idx)
	entries, _ := s.ListAudit(ctx, AuditFilter{LaneID: "l"})
	if version != len(migrations) || err != nil || len(entries) != 1 {
		t.Fatalf("version %d, index %q (%v), entries %d", version, idx, err, len(entries))
	}
	if err := s.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE name = 'audit_lane_ts'").Scan(&idx); err != sql.ErrNoRows {
		t.Fatalf("old index still present: %v", err)
	}
}
