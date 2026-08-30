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

	"github.com/go-macos/servicemanagement"
)

// macOS is an arranged machine: a bundle or not, an SMAppService that answers
// or refuses. None of these states can be reached on a developer's machine —
// least of all "this process is inside an application bundle", which a `go
// test` binary can never be — and every one of them is a different mechanism
// choice, which is the whole of what this file decides.
type macOS struct {
	bundle   string
	status   servicemanagement.Status
	statErr  error
	regErr   error
	unregErr error

	calls []string
}

func (m *macOS) install(t *testing.T) *macOS {
	t.Helper()
	sb, sr, su, ss := bundled, smRegister, smUnregist, smStatusOf
	t.Cleanup(func() { bundled, smRegister, smUnregist, smStatusOf = sb, sr, su, ss })

	bundled = func() (string, bool) { return m.bundle, m.bundle != "" }
	smRegister = func(p string) error { m.calls = append(m.calls, "register "+p); return m.regErr }
	smUnregist = func(p string) error { m.calls = append(m.calls, "unregister "+p); return m.unregErr }
	smStatusOf = func(p string) (servicemanagement.Status, error) {
		m.calls = append(m.calls, "status "+p)
		return m.status, m.statErr
	}
	return m
}

// bare is a process that is not in a bundle: the case every command-line
// program is in, and the one the plist exists for.
func bare() *macOS { return &macOS{} }

// inBundle is a bundled application with a working SMAppService.
func inBundle(st servicemanagement.Status) *macOS {
	return &macOS{bundle: "io.example.godl", status: st}
}

var spec = Spec{Label: "io.example.godl", Program: "/usr/local/bin/godl", RunAtLoad: true}

// TestOutsideABundleItIsThePlist covers the case that has to keep working
// unchanged. A command-line program gained nothing from macOS 13 and must lose
// nothing to it either.
func TestOutsideABundleItIsThePlist(t *testing.T) {
	home := ownHome(t)
	m := bare().install(t)

	reg, err := Enable(spec)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Method != MethodPlist || !reg.Enabled {
		t.Errorf("Enable = %+v, want an enabled plist", reg)
	}
	want := filepath.Join(home, "Library", "LaunchAgents", "io.example.godl.plist")
	if reg.Path != want {
		t.Errorf("Path = %s, want %s", reg.Path, want)
	}
	if reg.Fallback != nil {
		t.Errorf("a fallback was reported where nothing was tried: %v", reg.Fallback)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("no plist was written: %v", err)
	}

	st, err := State(spec.Label)
	if err != nil {
		t.Fatal(err)
	}
	if st.Method != MethodPlist || !st.Enabled || st.Path != want {
		t.Errorf("State = %+v", st)
	}

	if err := Disable(spec.Label); err != nil {
		t.Fatal(err)
	}
	st, err = State(spec.Label)
	if err != nil {
		t.Fatal(err)
	}
	if st.Enabled {
		t.Error("still enabled after Disable")
	}

	// Nothing was asked of SMAppService, because there is nothing to ask it
	// from. Asking anyway would be a program pretending it is an application.
	if len(m.calls) != 0 {
		t.Errorf("SMAppService was used outside a bundle: %v", m.calls)
	}
}

// TestInsideABundleItIsSMAppService covers the preference. Inside a bundle the
// supported mechanism wins, and no plist is written at all — two registrations
// for one program is a program that cannot be turned off.
func TestInsideABundleItIsSMAppService(t *testing.T) {
	home := ownHome(t)
	m := inBundle(servicemanagement.Enabled).install(t)

	reg, err := Enable(spec)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Method != MethodAppService {
		t.Fatalf("Enable used %v inside a bundle", reg.Method)
	}
	if !reg.Enabled {
		t.Error("an enabled service was reported as not enabled")
	}
	if reg.Advice != "" {
		t.Errorf("advice was given where there is nothing to do: %q", reg.Advice)
	}
	if reg.Fallback != nil {
		t.Errorf("a fallback was reported after a success: %v", reg.Fallback)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "io.example.godl.plist")); err == nil {
		t.Error("a plist was written as well as the registration; the program now has two")
	}

	st, err := State(spec.Label)
	if err != nil {
		t.Fatal(err)
	}
	if st.Method != MethodAppService || !st.Enabled {
		t.Errorf("State = %+v", st)
	}

	if err := Disable(spec.Label); err != nil {
		t.Fatal(err)
	}
	// The plist name is the label with .plist on the end — what a bundle must
	// ship in Contents/Library/LaunchAgents. A mismatch here registers
	// nothing and reports success.
	for _, c := range m.calls {
		if !strings.HasSuffix(c, " io.example.godl.plist") {
			t.Errorf("SMAppService was given %q", c)
		}
	}
	if !contains(m.calls, "unregister io.example.godl.plist") {
		t.Errorf("Disable did not unregister: %v", m.calls)
	}
}

// TestApprovalIsCarriedAllTheWayUp is why Registration has an Advice at all. A
// registration macOS is holding but will not run is the outcome nobody thinks
// to handle, and the caller cannot say anything useful about it unless this
// layer passes it on.
func TestApprovalIsCarriedAllTheWayUp(t *testing.T) {
	ownHome(t)
	inBundle(servicemanagement.RequiresApproval).install(t)

	reg, err := Enable(spec)
	if err != nil {
		t.Fatalf("Enable = %v; awaiting approval is not a failure", err)
	}
	if reg.Method != MethodAppService {
		t.Fatalf("Enable used %v", reg.Method)
	}
	// It is registered and it will NOT run. Reporting Enabled here would be
	// this package telling the caller a program starts at login when it does
	// not.
	if reg.Enabled {
		t.Error("a service awaiting approval was reported as enabled")
	}
	if !strings.Contains(reg.Advice, "System Settings") {
		t.Errorf("the advice does not say where to go: %q", reg.Advice)
	}

	st, err := State(spec.Label)
	if err != nil {
		t.Fatal(err)
	}
	if st.Method != MethodAppService || st.Enabled || st.Advice == "" {
		t.Errorf("State = %+v", st)
	}
}

// TestARefusalFallsBackAndSaysSo covers the case the whole design turns on: a
// bundle that does not ship the plist. Failing here would stop a working
// program working the day it gained a bundle; falling back SILENTLY would hide
// a real defect forever.
func TestARefusalFallsBackAndSaysSo(t *testing.T) {
	home := ownHome(t)
	m := inBundle(servicemanagement.NotFound).install(t)
	m.regErr = errors.New("Unable to read plist: io.example.godl.plist")

	reg, err := Enable(spec)
	if err != nil {
		t.Fatalf("Enable = %v; a refusal must fall back, not fail", err)
	}
	if reg.Method != MethodPlist || !reg.Enabled {
		t.Errorf("Enable = %+v, want the plist", reg)
	}
	if reg.Fallback == nil || !strings.Contains(reg.Fallback.Error(), "Unable to read plist") {
		t.Errorf("the refusal was swallowed: %+v", reg)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "io.example.godl.plist")); err != nil {
		t.Errorf("no plist was written after the fallback: %v", err)
	}

	// And State falls through to it: SMAppService holds no registration, so
	// the plist is what is really there.
	st, err := State(spec.Label)
	if err != nil {
		t.Fatal(err)
	}
	if st.Method != MethodPlist || !st.Enabled {
		t.Errorf("State = %+v, want the plist", st)
	}
}

// TestDisableTakesAwayBoth covers a program that shipped a plist and then
// gained a bundle. Both registrations exist, macOS acts on the SMAppService
// one, and removing only that leaves the program still starting at login — the
// bug a person would describe as "I turned it off and it came back".
func TestDisableTakesAwayBoth(t *testing.T) {
	home := ownHome(t)
	bare().install(t)
	if _, err := Enable(spec); err != nil { // the plist, from before the bundle
		t.Fatal(err)
	}
	m2 := inBundle(servicemanagement.Enabled).install(t) // and now a bundle

	if err := Disable(spec.Label); err != nil {
		t.Fatal(err)
	}
	if !contains(m2.calls, "unregister io.example.godl.plist") {
		t.Errorf("the registration was left behind: %v", m2.calls)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "io.example.godl.plist")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the plist was left behind: %v", err)
	}
}

// TestNothingRegisteredIsNotAnError covers the no-op. macOS reports
// -unregister: on something never registered as an error (SMAppServiceErrorDomain
// 22, "Invalid argument"), so the status is read first — and a Disable that
// found nothing to do must succeed.
func TestNothingRegisteredIsNotAnError(t *testing.T) {
	ownHome(t)
	m := inBundle(servicemanagement.NotRegistered).install(t)
	m.unregErr = errors.New("Invalid argument")

	if err := Disable(spec.Label); err != nil {
		t.Fatalf("Disable = %v when there was nothing to disable", err)
	}
	if contains(m.calls, "unregister io.example.godl.plist") {
		t.Errorf("-unregister: was sent for a service that was never registered: %v", m.calls)
	}
}

// TestWhenMacOSWillNotAnswer covers every failure that belongs to the machine.
// A startup setting half applied is worse than none, because it looks applied.
func TestWhenMacOSWillNotAnswer(t *testing.T) {
	refused := errors.New("SMAppService refused")

	t.Run("a status nobody could read", func(t *testing.T) {
		ownHome(t)
		m := inBundle(servicemanagement.Enabled).install(t)
		m.statErr = refused
		if _, err := Enable(spec); !errors.Is(err, refused) {
			t.Errorf("Enable = %v", err)
		}
		if _, err := State(spec.Label); !errors.Is(err, refused) {
			t.Errorf("State = %v", err)
		}
		if err := Disable(spec.Label); !errors.Is(err, refused) {
			t.Errorf("Disable = %v", err)
		}
	})

	t.Run("an unregister that was refused", func(t *testing.T) {
		ownHome(t)
		m := inBundle(servicemanagement.Enabled).install(t)
		m.unregErr = refused
		if err := Disable(spec.Label); !errors.Is(err, refused) {
			t.Errorf("Disable = %v", err)
		}
	})

	t.Run("a plist that cannot be written", func(t *testing.T) {
		ownHome(t)
		bare().install(t)
		saved := writeFile
		writeFile = func(string, []byte, os.FileMode) error { return errors.New("the disk is full") }
		t.Cleanup(func() { writeFile = saved })
		if _, err := Enable(spec); err == nil {
			t.Error("an agent that was never written was reported enabled")
		}
	})

	t.Run("no home to look in", func(t *testing.T) {
		bare().install(t)
		saved := homeDir
		homeDir = func() (string, error) { return "", errors.New("no home here") }
		t.Cleanup(func() { homeDir = saved })
		if _, err := State(spec.Label); err == nil {
			t.Error("State answered without a home")
		}
	})

	t.Run("a plist that cannot be looked at", func(t *testing.T) {
		ownHome(t)
		bare().install(t)
		saved := stat
		stat = func(string) (os.FileInfo, error) { return nil, errors.New("refused") }
		t.Cleanup(func() { stat = saved })
		if _, err := State(spec.Label); err == nil {
			t.Error("a question nobody could answer came back as no")
		}
	})

	t.Run("a label that cannot be a file name", func(t *testing.T) {
		ownHome(t)
		bare().install(t)
		for _, bad := range []string{"", "io/example", "."} {
			if _, err := Enable(Spec{Label: bad, Program: "/bin/true"}); err == nil {
				t.Errorf("Enable(%q) answered", bad)
			}
			if _, err := State(bad); err == nil {
				t.Errorf("State(%q) answered", bad)
			}
			if err := Disable(bad); err == nil {
				t.Errorf("Disable(%q) answered", bad)
			}
		}
	})
}

// TestAMethodNamesItself covers the rendering. It goes in logs and in what a
// program tells a person, and "Method(1)" tells them nothing.
func TestAMethodNamesItself(t *testing.T) {
	if got := MethodPlist.String(); got != "plist" {
		t.Errorf("MethodPlist = %q", got)
	}
	if got := MethodAppService.String(); got != "SMAppService" {
		t.Errorf("MethodAppService = %q", got)
	}
}

// contains reports whether ss holds s.
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// TestTheSeamsReallyReachServiceManagement is the negative control for every
// other test in this file. All of them replace the seams, so all of them would
// keep passing if the seams were wired to nothing at all — or to the wrong
// service.
//
// Here the DEFAULTS are called: the real go-macos/servicemanagement. Nothing
// is registered and nothing can be, on any platform. Off darwin the package
// answers ErrUnsupported before it touches an OS; on darwin a `go test` binary
// is a bare executable, so it answers ErrNotBundled before it touches
// SMAppService. Either way the call stops at the guard, which is the point —
// the wiring is exercised and the machine is not.
func TestTheSeamsReallyReachServiceManagement(t *testing.T) {
	if _, ok := bundled(); ok {
		t.Fatal("a `go test` binary reported itself as being inside an application bundle")
	}
	const name = "io.example.go-macos.launchagent.doesnotexist.plist"
	for _, tc := range []struct {
		what string
		err  error
	}{
		{"register", smRegister(name)},
		{"unregister", smUnregist(name)},
		{"status", second(smStatusOf(name))},
	} {
		if tc.err == nil {
			t.Errorf("%s reached macOS from a bare executable and succeeded", tc.what)
			continue
		}
		if !errors.Is(tc.err, servicemanagement.ErrNotBundled) &&
			!errors.Is(tc.err, servicemanagement.ErrUnsupported) {
			t.Errorf("%s = %v, want ErrNotBundled or ErrUnsupported", tc.what, tc.err)
		}
	}
}

// second drops a status and keeps the error, so the table above can hold all
// three seams even though one of them answers a value as well.
func second(_ servicemanagement.Status, err error) error { return err }
