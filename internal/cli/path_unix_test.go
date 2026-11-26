//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jtsang4/lanesh/internal/daemon"
)

func setupFakeManagedBinary(t *testing.T) (home string, p daemon.Paths) {
	t.Helper()
	home = t.TempDir()
	p = daemon.Paths{Home: filepath.Join(home, ".lanesh")}
	if err := os.MkdirAll(p.BinDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Binary(), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, p
}

func TestExposeUnixSymlinkInLocalBin(t *testing.T) {
	home, p := setupFakeManagedBinary(t)
	localBin := filepath.Join(home, ".local", "bin")
	// Note: localBin does not exist yet, but IS on PATH — exposeUnix should create it and link.
	res := exposeUnix(p, unixPathEnv{
		home:       home,
		pathEnv:    localBin + ":/usr/bin",
		shell:      "/bin/zsh",
		goos:       "linux",
		candidates: []string{localBin, filepath.Join(home, "bin")},
	})
	if res.Kind != pathLinked {
		t.Fatalf("Kind = %v, want pathLinked", res.Kind)
	}
	wantLink := filepath.Join(localBin, "lanesh")
	if res.LinkPath != wantLink {
		t.Fatalf("LinkPath = %q, want %q", res.LinkPath, wantLink)
	}
	dest, err := os.Readlink(wantLink)
	if err != nil {
		t.Fatal(err)
	}
	if dest != p.Binary() {
		t.Fatalf("symlink target = %q, want %q", dest, p.Binary())
	}
	// Running a second time must be a silent no-op.
	res2 := exposeUnix(p, unixPathEnv{
		home:       home,
		pathEnv:    localBin + ":/usr/bin",
		shell:      "/bin/zsh",
		goos:       "linux",
		candidates: []string{localBin, filepath.Join(home, "bin")},
	})
	if res2.Kind != pathNoop {
		t.Fatalf("second run Kind = %v, want pathNoop", res2.Kind)
	}
	// Must not have created ~/.zshrc when symlink succeeded.
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatalf("expected no .zshrc modification when symlink succeeded")
	}
}

func TestExposeUnixSkipsForeignFileAndHomebrew(t *testing.T) {
	home, p := setupFakeManagedBinary(t)
	localBin := filepath.Join(home, ".local", "bin")
	brewBin := filepath.Join(home, "fake-homebrew", "bin")
	userBin := filepath.Join(home, "bin")
	for _, d := range []string{localBin, brewBin, userBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Foreign regular file in ~/.local/bin must NOT be overwritten.
	foreign := filepath.Join(localBin, "lanesh")
	if err := os.WriteFile(foreign, []byte("#!/bin/sh\necho foreign\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake Homebrew directory contains brew binary and must be skipped.
	if err := os.WriteFile(filepath.Join(brewBin, "brew"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := exposeUnix(p, unixPathEnv{
		home:       home,
		pathEnv:    strings.Join([]string{brewBin, localBin, userBin}, ":"),
		shell:      "/bin/zsh",
		goos:       "darwin",
		candidates: []string{brewBin, localBin, userBin},
	})
	if res.Kind != pathLinked {
		t.Fatalf("Kind = %v, want pathLinked", res.Kind)
	}
	if res.LinkPath != filepath.Join(userBin, "lanesh") {
		t.Fatalf("LinkPath = %q, want %q", res.LinkPath, filepath.Join(userBin, "lanesh"))
	}
	if res.Shadowed != foreign {
		t.Fatalf("Shadowed = %q, want %q", res.Shadowed, foreign)
	}
	// Verify foreign file untouched and brewBin untouched.
	got, _ := os.ReadFile(foreign)
	if !strings.Contains(string(got), "foreign") {
		t.Fatalf("foreign file was overwritten: %q", string(got))
	}
	if _, err := os.Lstat(filepath.Join(brewBin, "lanesh")); !os.IsNotExist(err) {
		t.Fatalf("homebrew bin directory must not be modified")
	}
}

func TestExposeUnixReplacesDanglingLaneshSymlink(t *testing.T) {
	home, p := setupFakeManagedBinary(t)
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(localBin, "lanesh")
	if err := os.Symlink("/nonexistent/old/.lanesh/bin/lanesh", linkPath); err != nil {
		t.Fatal(err)
	}
	res := exposeUnix(p, unixPathEnv{
		home:       home,
		pathEnv:    localBin,
		shell:      "/bin/bash",
		goos:       "linux",
		candidates: []string{localBin},
	})
	if res.Kind != pathLinked {
		t.Fatalf("Kind = %v, want pathLinked", res.Kind)
	}
	dest, err := os.Readlink(linkPath)
	if err != nil || dest != p.Binary() {
		t.Fatalf("Readlink = %q (%v), want %q", dest, err, p.Binary())
	}
}

func TestExposeUnixShellProfileFallbackAndIdempotency(t *testing.T) {
	cases := []struct {
		name        string
		shell       string
		goos        string
		zdotdir     string
		xdgConfig   string
		wantRelPath string
		wantLine    string
		wantReload  string
		preContent  string
	}{
		{
			name:        "zsh default",
			shell:       "/bin/zsh",
			goos:        "darwin",
			wantRelPath: ".zshrc",
			wantLine:    `export PATH="$HOME/.lanesh/bin:$PATH"`,
			wantReload:  "source ~/.zshrc",
			preContent:  "# existing without trailing newline",
		},
		{
			name:        "zsh custom ZDOTDIR",
			shell:       "/usr/bin/zsh",
			goos:        "linux",
			zdotdir:     "zshcfg",
			wantRelPath: filepath.Join("zshcfg", ".zshrc"),
			wantLine:    `export PATH="$HOME/.lanesh/bin:$PATH"`,
			wantReload:  "source ~/zshcfg/.zshrc",
		},
		{
			name:        "bash on darwin",
			shell:       "/bin/bash",
			goos:        "darwin",
			wantRelPath: ".bash_profile",
			wantLine:    `export PATH="$HOME/.lanesh/bin:$PATH"`,
			wantReload:  "source ~/.bash_profile",
		},
		{
			name:        "bash on linux ignores commented entry",
			shell:       "/bin/bash",
			goos:        "linux",
			wantRelPath: ".bashrc",
			wantLine:    `export PATH="$HOME/.lanesh/bin:$PATH"`,
			wantReload:  "source ~/.bashrc",
			preContent:  "# export PATH=\"$HOME/.lanesh/bin:$PATH\"\n",
		},
		{
			name:        "fish",
			shell:       "/usr/bin/fish",
			goos:        "linux",
			xdgConfig:   "xdg",
			wantRelPath: filepath.Join("xdg", "fish", "config.fish"),
			wantLine:    `set -gx PATH "$HOME/.lanesh/bin" $PATH`,
			wantReload:  "source ~/xdg/fish/config.fish",
		},
		{
			name:        "posix sh fallback",
			shell:       "/bin/sh",
			goos:        "linux",
			wantRelPath: ".profile",
			wantLine:    `export PATH="$HOME/.lanesh/bin:$PATH"`,
			wantReload:  ". ~/.profile",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, p := setupFakeManagedBinary(t)
			rcPath := filepath.Join(home, c.wantRelPath)
			if c.preContent != "" {
				if err := os.MkdirAll(filepath.Dir(rcPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(rcPath, []byte(c.preContent), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			env := unixPathEnv{
				home:       home,
				pathEnv:    "/usr/bin:/bin",
				shell:      c.shell,
				goos:       c.goos,
				candidates: []string{filepath.Join(home, ".local", "bin")}, // not on PATH
			}
			if c.zdotdir != "" {
				env.zdotdir = filepath.Join(home, c.zdotdir)
			}
			if c.xdgConfig != "" {
				env.xdgConfigHome = filepath.Join(home, c.xdgConfig)
			}

			res := exposeUnix(p, env)
			if res.Kind != pathProfileAdded {
				t.Fatalf("Kind = %v, want pathProfileAdded", res.Kind)
			}
			if res.ReloadCmd != c.wantReload {
				t.Fatalf("ReloadCmd = %q, want %q", res.ReloadCmd, c.wantReload)
			}
			body, err := os.ReadFile(rcPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), c.wantLine) {
				t.Fatalf("profile missing %q:\n%s", c.wantLine, string(body))
			}
			if c.preContent != "" && !strings.HasPrefix(string(body), c.preContent+"\n") {
				t.Fatalf("existing content without newline was not separated:\n%q", string(body))
			}

			// Second run must be idempotent and not duplicate the entry.
			res2 := exposeUnix(p, env)
			if res2.Kind != pathProfileExists {
				t.Fatalf("second run Kind = %v, want pathProfileExists", res2.Kind)
			}
			body2, _ := os.ReadFile(rcPath)
			if string(body2) != string(body) {
				t.Fatalf("second run modified profile:\nbefore:\n%s\nafter:\n%s", string(body), string(body2))
			}
		})
	}
}

func TestExposeUnixNPMGlobalVsEphemeralNPX(t *testing.T) {
	t.Run("global npm install is treated as already exposed", func(t *testing.T) {
		home, p := setupFakeManagedBinary(t)
		npmPkgBin := filepath.Join(home, "nvm", "lib", "node_modules", "lanesh", "bin")
		npmGlobalBin := filepath.Join(home, "nvm", "bin")
		for _, d := range []string{npmPkgBin, npmGlobalBin} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		js := filepath.Join(npmPkgBin, "lanesh.js")
		if err := os.WriteFile(js, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(js, filepath.Join(npmGlobalBin, "lanesh")); err != nil {
			t.Fatal(err)
		}

		localBin := filepath.Join(home, ".local", "bin")
		res := exposeUnix(p, unixPathEnv{
			home:       home,
			pathEnv:    npmGlobalBin + ":" + localBin,
			shell:      "/bin/zsh",
			goos:       "darwin",
			candidates: []string{localBin},
		})
		if res.Kind != pathNoop {
			t.Fatalf("Kind = %v, want pathNoop", res.Kind)
		}
		if _, err := os.Lstat(filepath.Join(localBin, "lanesh")); !os.IsNotExist(err) {
			t.Fatal("should not create symlink when global npm launcher is on PATH")
		}
	})

	t.Run("ephemeral npx shim is ignored and not reported as shadowing", func(t *testing.T) {
		home, p := setupFakeManagedBinary(t)
		npxBin := filepath.Join(home, ".npm", "_npx", "abc123", "node_modules", ".bin")
		localBin := filepath.Join(home, ".local", "bin")
		for _, d := range []string{npxBin, localBin} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(npxBin, "lanesh"), []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		res := exposeUnix(p, unixPathEnv{
			home:       home,
			pathEnv:    npxBin + ":" + localBin,
			shell:      "/bin/zsh",
			goos:       "linux",
			candidates: []string{localBin},
		})
		if res.Kind != pathLinked {
			t.Fatalf("Kind = %v, want pathLinked", res.Kind)
		}
		if res.Shadowed != "" {
			t.Fatalf("Shadowed = %q, want empty (ephemeral npx shim must be ignored)", res.Shadowed)
		}
	})
}

func TestExposeUnixIsolationGuards(t *testing.T) {
	t.Run("LANESH_NO_MODIFY_PATH skips all changes", func(t *testing.T) {
		home, p := setupFakeManagedBinary(t)
		localBin := filepath.Join(home, ".local", "bin")
		res := exposeUnix(p, unixPathEnv{
			home:       home,
			pathEnv:    localBin,
			shell:      "/bin/zsh",
			goos:       "darwin",
			noModify:   true,
			candidates: []string{localBin},
		})
		if res.Kind != pathSkipped {
			t.Fatalf("Kind = %v, want pathSkipped", res.Kind)
		}
		if _, err := os.Stat(localBin); !os.IsNotExist(err) {
			t.Fatal("must not create localBin when noModify is set")
		}
		if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
			t.Fatal("must not touch .zshrc when noModify is set")
		}
	})

	t.Run("custom LANESH_HOME skips all changes", func(t *testing.T) {
		home := t.TempDir()
		custom := daemon.Paths{Home: filepath.Join(home, "custom-home")}
		if err := os.MkdirAll(custom.BinDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(custom.Binary(), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		localBin := filepath.Join(home, ".local", "bin")
		res := exposeUnix(custom, unixPathEnv{
			home:       home,
			pathEnv:    localBin,
			shell:      "/bin/zsh",
			goos:       "darwin",
			candidates: []string{localBin},
		})
		if res.Kind != pathSkipped {
			t.Fatalf("Kind = %v, want pathSkipped", res.Kind)
		}
		if _, err := os.Stat(localBin); !os.IsNotExist(err) {
			t.Fatal("must not create symlink for custom LANESH_HOME")
		}
	})
}
