package cli

import (
	"path/filepath"
	"testing"

	"github.com/jtsang4/lanesh/internal/daemon"
)

func TestModifyPathDisabled(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"  False ", false},
		{"1", true},
		{"true", true},
		{"yes", true},
	}
	for _, c := range cases {
		if got := modifyPathDisabled(c.val); got != c.want {
			t.Errorf("modifyPathDisabled(%q) = %v, want %v", c.val, got, c.want)
		}
	}
}

func TestIsDefaultHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user")
	def := daemon.Paths{Home: filepath.Join(home, ".lanesh")}
	custom := daemon.Paths{Home: filepath.Join(home, "custom-lanesh")}
	if !isDefaultHome(def, home) {
		t.Fatalf("expected %q to match default home under %q", def.Home, home)
	}
	if isDefaultHome(custom, home) {
		t.Fatalf("expected %q not to match default home under %q", custom.Home, home)
	}
	if isDefaultHome(def, "") {
		t.Fatal("empty userHome must return false")
	}
}

func TestAddWindowsPathEntry(t *testing.T) {
	cases := []struct {
		name      string
		existing  string
		binDir    string
		wantPath  string
		wantAdded bool
	}{
		{
			name:      "empty path",
			existing:  "",
			binDir:    `C:\Users\alice\.lanesh\bin`,
			wantPath:  `C:\Users\alice\.lanesh\bin`,
			wantAdded: true,
		},
		{
			name:      "prepends to existing entries",
			existing:  `C:\Windows\System32;C:\Tools`,
			binDir:    `C:\Users\alice\.lanesh\bin`,
			wantPath:  `C:\Users\alice\.lanesh\bin;C:\Windows\System32;C:\Tools`,
			wantAdded: true,
		},
		{
			name:      "detects case-insensitive duplicate with trailing slash",
			existing:  `C:\Tools;c:\users\alice\.lanesh\bin\`,
			binDir:    `C:\Users\alice\.lanesh\bin`,
			wantPath:  `C:\Tools;c:\users\alice\.lanesh\bin\`,
			wantAdded: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPath, gotAdded := addWindowsPathEntry(c.existing, c.binDir)
			if gotAdded != c.wantAdded || gotPath != c.wantPath {
				t.Errorf("addWindowsPathEntry(%q, %q) = (%q, %v), want (%q, %v)",
					c.existing, c.binDir, gotPath, gotAdded, c.wantPath, c.wantAdded)
			}
		})
	}
}

func TestIsEphemeralLauncherPath(t *testing.T) {
	cases := []struct {
		p    string
		want bool
	}{
		{"/home/alice/.npm/_npx/abcd/node_modules/.bin/lanesh", true},
		{"/home/alice/project/node_modules/.bin/lanesh", true},
		{"/tmp/bunx-1000-lanesh/bin/lanesh", true},
		{"/home/alice/.local/share/pnpm/lanesh", false},
		{"/home/alice/.nvm/versions/node/v24.0.0/bin/lanesh", false},
	}
	for _, c := range cases {
		if got := isEphemeralLauncherPath(c.p); got != c.want {
			t.Errorf("isEphemeralLauncherPath(%q) = %v, want %v", c.p, got, c.want)
		}
	}
}
