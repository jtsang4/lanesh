package daemon

import (
	"testing"
)

func TestParseLaneURL(t *testing.T) {
	cases := []struct {
		in, server, lane string
		ok               bool
	}{
		{"https://lanesh.example.com/lanes/ln_abc", "https://lanesh.example.com", "ln_abc", true},
		{"https://example.com/prefix/lanes/ln_abc/", "https://example.com/prefix", "ln_abc", true},
		{"http://127.0.0.1:8080/lanes/ln_x", "http://127.0.0.1:8080", "ln_x", true},
		{"http://example.com/lanes/ln_x", "", "", false},
		{"https://example.com/nope/ln_x", "", "", false},
		{"ftp://example.com/lanes/ln_x", "", "", false},
	}
	for _, c := range cases {
		server, lane, err := ParseLaneURL(c.in)
		if (err == nil) != c.ok || server != c.server || lane != c.lane {
			t.Errorf("ParseLaneURL(%q) = %q, %q, %v", c.in, server, lane, err)
		}
	}
}

func TestConfigRoundTripAndPolicy(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	off := false
	cfg := Config{
		Policy: PolicyConfig{AllowExec: &off},
		Lanes:  []LaneConfig{{Server: "https://s", LaneID: "ln_1", LaneName: "work"}, {Server: "https://s", LaneID: "ln_2", Policy: PolicyConfig{AllowExec: ptr(true)}}},
	}
	if err := SaveConfig(p, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.EffectivePolicy(got.Lanes[0]).AllowExec || !got.EffectivePolicy(got.Lanes[1]).AllowExec || !got.EffectivePolicy(got.Lanes[0]).ExposeSkills {
		t.Fatalf("policies wrong: %+v", got)
	}
	if got.FindLane("work") != 0 || got.FindLane("https://s/lanes/ln_2") != 1 || got.FindLane("x") != -1 {
		t.Fatal("FindLane wrong")
	}
	id1, _ := MachineID(p)
	id2, _ := MachineID(p)
	if id1 == "" || id1 != id2 {
		t.Fatal("machine id must persist")
	}
	if err := SaveCredentials(p, map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	creds, _ := LoadCredentials(p)
	if creds["k"] != "v" {
		t.Fatal("credentials round trip failed")
	}
}

func ptr[T any](v T) *T { return &v }
