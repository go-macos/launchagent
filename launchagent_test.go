// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package launchagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ownHome points the package at a directory of this test's own, so a run here
// can never write into the home of whoever is running the tests.
func ownHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	saved := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = saved })
	return home
}

// TestAnAgentHoldsWhatLaunchdReads covers the plist. Every key in it is read
// by the system rather than by us, so a missing one is not a failure anybody
// sees — it is a service that behaves oddly, or never starts.
func TestAnAgentHoldsWhatLaunchdReads(t *testing.T) {
	home := ownHome(t)
	path, err := Install(Spec{
		Label: "io.example.godl", Program: "/usr/local/bin/godl",
		Args: []string{"queue", "run"}, WorkingDir: "/tmp",
		RunAtLoad: true, KeepAlive: true,
		StdoutPath: "/tmp/out.log", StderrPath: "/tmp/err.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Library", "LaunchAgents", "io.example.godl.plist"); path != want {
		t.Errorf("written to %s, want %s", path, want)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<key>Label</key>", "io.example.godl",
		"<key>ProgramArguments</key>", "/usr/local/bin/godl", "<string>queue</string>", "<string>run</string>",
		"<key>WorkingDirectory</key>", "<key>StandardOutPath</key>", "<key>StandardErrorPath</key>",
		"<key>RunAtLoad</key>", "<key>KeepAlive</key>", "<true/>",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the plist does not say %q:\n%s", want, raw)
		}
	}

	if ok, err := Installed("io.example.godl"); err != nil || !ok {
		t.Errorf("Installed = %v, %v", ok, err)
	}
	if err := Remove("io.example.godl"); err != nil {
		t.Fatal(err)
	}
	if ok, err := Installed("io.example.godl"); err != nil || ok {
		t.Errorf("after Remove, Installed = %v, %v", ok, err)
	}
	// Asking for it to be gone and finding it gone is the outcome wanted.
	if err := Remove("io.example.godl"); err != nil {
		t.Errorf("removing what is not there: %v", err)
	}
}

// TestAnAgentSaysOnlyWhatItWasTold covers the optional keys. A plist naming a
// log file nobody asked for, or keeping alive a program meant to finish, is a
// service fighting its own author.
func TestAnAgentSaysOnlyWhatItWasTold(t *testing.T) {
	ownHome(t)
	path, err := Install(Spec{Label: "io.example.plain", Program: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	for _, absent := range []string{"WorkingDirectory", "StandardOutPath", "StandardErrorPath", "RunAtLoad", "KeepAlive"} {
		if strings.Contains(string(raw), absent) {
			t.Errorf("the plist volunteers %s:\n%s", absent, raw)
		}
	}
	// XML, so a path with an ampersand in it must not end the document early.
	p2, err := Install(Spec{Label: "io.example.amp", Program: "/opt/rock & roll/godl"})
	if err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(p2)
	if !strings.Contains(string(raw2), "rock &amp; roll") {
		t.Errorf("an ampersand went in raw:\n%s", raw2)
	}
}

// TestALabelThatCannotBeAFileName covers the refusals. A label reaches the
// filesystem as a path, so one carrying a separator writes the agent where
// launchd will never look — a service that silently never runs.
func TestALabelThatCannotBeAFileName(t *testing.T) {
	ownHome(t)
	for _, bad := range []string{"", "io/example", `io\example`, ".", ".."} {
		if _, err := Install(Spec{Label: bad, Program: "/bin/true"}); err == nil {
			t.Errorf("an agent was installed as %q", bad)
		}
		if _, err := Path(bad); err == nil {
			t.Errorf("Path(%q) answered", bad)
		}
		if err := Remove(bad); err == nil {
			t.Errorf("Remove(%q) answered", bad)
		}
		if _, err := Installed(bad); err == nil {
			t.Errorf("Installed(%q) answered", bad)
		}
	}
	if _, err := Install(Spec{Label: "io.example.nothing"}); err == nil {
		t.Error("an agent with nothing to run was installed")
	}
}

// TestAMachineThatWillNotCooperate covers every failure that belongs to the
// machine rather than to the path. None can be arranged on a working one,
// which is exactly why they are staged: a service half-installed is worse than
// none, because it looks installed.
func TestAMachineThatWillNotCooperate(t *testing.T) {
	full := errors.New("the disk is full")
	spec := Spec{Label: "io.example.godl", Program: "/bin/true"}

	t.Run("no home to put it in", func(t *testing.T) {
		saved := homeDir
		homeDir = func() (string, error) { return "", errors.New("no home here") }
		t.Cleanup(func() { homeDir = saved })
		if _, err := Dir(); err == nil {
			t.Error("Dir answered without a home")
		}
		if _, err := Install(spec); err == nil {
			t.Error("an agent was installed with nowhere to live")
		}
		if err := Remove(spec.Label); err == nil {
			t.Error("Remove answered without a home")
		}
		if _, err := Installed(spec.Label); err == nil {
			t.Error("Installed answered without a home")
		}
	})

	t.Run("the directory cannot be made", func(t *testing.T) {
		ownHome(t)
		saved := mkdirAll
		mkdirAll = func(string, os.FileMode) error { return full }
		t.Cleanup(func() { mkdirAll = saved })
		if _, err := Install(spec); err == nil {
			t.Error("an agent was installed with nowhere to put it")
		}
	})

	t.Run("it cannot be written", func(t *testing.T) {
		ownHome(t)
		saved := writeFile
		writeFile = func(string, []byte, os.FileMode) error { return full }
		t.Cleanup(func() { writeFile = saved })
		if _, err := Install(spec); err == nil {
			t.Error("an agent that was never written was reported installed")
		}
	})

	t.Run("it cannot be removed", func(t *testing.T) {
		ownHome(t)
		saved := remove
		remove = func(string) error { return errors.New("read-only") }
		t.Cleanup(func() { remove = saved })
		if err := Remove(spec.Label); err == nil {
			t.Error("an agent that is still there was reported removed")
		}
	})

	t.Run("it cannot be looked at", func(t *testing.T) {
		ownHome(t)
		saved := stat
		stat = func(string) (os.FileInfo, error) { return nil, errors.New("refused") }
		t.Cleanup(func() { stat = saved })
		if _, err := Installed(spec.Label); err == nil {
			t.Error("a question nobody could answer came back as no")
		}
	})
}
