// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

package launchagent

import (
	"fmt"

	"github.com/go-macos/servicemanagement"
)

// Method is the mechanism actually holding a startup registration.
//
// It is reported rather than assumed because the two are not interchangeable
// and a caller has things to say about which one it got: a plist is invisible
// to the person who owns the machine, and an SMAppService registration can be
// switched off by them at any moment.
type Method int

const (
	// MethodPlist is a plist in ~/Library/LaunchAgents — what [Install]
	// writes. It works for anything, including a bare executable in
	// /usr/local/bin, and macOS shows it to the person as a reverse-DNS label
	// with no name and no icon.
	MethodPlist Method = iota
	// MethodAppService is SMAppService, the supported path since macOS 13. It
	// requires an application bundle, it appears in System Settings under the
	// application's own name, and the person can switch it off there — which
	// [State] can then report.
	MethodAppService
)

// String names the method.
func (m Method) String() string {
	if m == MethodAppService {
		return "SMAppService"
	}
	return "plist"
}

// Registration is what [Enable] did, or what [State] found.
type Registration struct {
	// Method is the mechanism holding it.
	Method Method
	// Enabled reports whether the program will actually start at the next
	// login. It is false for a registration waiting to be approved: macOS
	// holds it and will not run it.
	Enabled bool
	// Path is the plist, when Method is [MethodPlist]. It is the path
	// [Install] returns, and it is set even when nothing is installed there,
	// so a caller can say where it looked.
	Path string
	// Advice is what to tell the person, or "" when there is nothing for them
	// to do. It is non-empty exactly when macOS is waiting on them — most
	// often because they switched this item off before, in which case it
	// stays off whatever this program does.
	Advice string
	// Fallback is the SMAppService failure that made [Enable] write a plist
	// instead, and nil when there was none.
	//
	// It is carried rather than swallowed. Falling back is the right
	// behaviour — a program that was working must not stop working the day it
	// gains a bundle — but a fallback is also how "the plist is not in the
	// bundle" looks from the outside, and a caller that never sees this error
	// never finds that out. Log it; do not fail on it.
	Fallback error
}

// Seams: everything that reaches macOS. They are variables so the whole
// mechanism-choosing logic below — which is where the decisions are — can be
// tested on any platform, including the case that cannot be arranged at all on
// a developer's machine: being inside a bundle.
var (
	bundled    = servicemanagement.Bundled
	smRegister = func(plist string) error { return servicemanagement.Agent(plist).Register() }
	smUnregist = func(plist string) error { return servicemanagement.Agent(plist).Unregister() }
	smStatusOf = func(plist string) (servicemanagement.Status, error) {
		return servicemanagement.Agent(plist).Status()
	}
)

// plistName is the file name SMAppService knows an agent by: the label with
// .plist on the end, which is exactly what [Install] writes into
// ~/Library/LaunchAgents and what a bundle must ship in
// Contents/Library/LaunchAgents.
func plistName(label string) string { return label + ".plist" }

// Enable makes the program start at login by whichever mechanism this process
// can actually use, and reports which one that was.
//
// Inside an application bundle it prefers SMAppService: it is what Apple
// supports, it puts the item in System Settings under the application's own
// name, and it lets the person turn it off — which is a feature, not a
// problem, provided the program can find out. Everywhere else, and whenever
// SMAppService refuses, it writes the plist [Install] writes.
//
// A refusal is reported in [Registration.Fallback] rather than returned as an
// error. The commonest one is a bundle that does not ship
// Contents/Library/LaunchAgents/LABEL.plist — a real defect, but not a reason
// to leave a program that used to start at login no longer starting at login.
//
// A nil error does NOT mean the program will run. Read [Registration.Enabled],
// and show [Registration.Advice] when it is there: a registration awaiting
// approval is held by macOS and idle, and saying nothing about it is how a
// person ends up with a feature that silently does nothing.
func Enable(s Spec) (Registration, error) {
	if err := checkLabel(s.Label); err != nil {
		return Registration{}, err
	}
	name := plistName(s.Label)

	if _, ok := bundled(); ok {
		if err := smRegister(name); err != nil {
			return plistFallback(s, err)
		}
		st, err := smStatusOf(name)
		if err != nil {
			return Registration{}, fmt.Errorf("launchagent: %w", err)
		}
		return Registration{
			Method:  MethodAppService,
			Enabled: st.Running(),
			Advice:  st.Advice(),
		}, nil
	}
	return plistFallback(s, nil)
}

// plistFallback writes the legacy agent, carrying whatever SMAppService said
// on the way past.
func plistFallback(s Spec, why error) (Registration, error) {
	path, err := Install(s)
	if err != nil {
		return Registration{}, err
	}
	return Registration{Method: MethodPlist, Enabled: true, Path: path, Fallback: why}, nil
}

// State reports whether the program is set to start at login, and how.
//
// It asks SMAppService first when there is a bundle to ask from, and falls
// through to the plist when macOS holds no registration. That order matters
// for a program that shipped a plist before it gained a bundle: both can exist
// at once, and the SMAppService one is the one macOS acts on.
func State(label string) (Registration, error) {
	if err := checkLabel(label); err != nil {
		return Registration{}, err
	}
	if _, ok := bundled(); ok {
		st, err := smStatusOf(plistName(label))
		if err != nil {
			return Registration{}, fmt.Errorf("launchagent: %w", err)
		}
		if st.Registered() {
			return Registration{
				Method:  MethodAppService,
				Enabled: st.Running(),
				Advice:  st.Advice(),
			}, nil
		}
	}
	path, err := Path(label)
	if err != nil {
		return Registration{}, err
	}
	ok, err := Installed(label)
	if err != nil {
		return Registration{}, err
	}
	return Registration{Method: MethodPlist, Enabled: ok, Path: path}, nil
}

// Disable stops the program starting at login, whichever way it is held — and
// it takes away BOTH, because a program that gained a bundle after shipping a
// plist has two registrations and removing one of them leaves it starting at
// login anyway.
//
// A registration that is not there is not an error: asking for it to be gone
// and finding it gone is the outcome wanted. SMAppService is asked for its
// status first for exactly that reason: -unregister: on something never
// registered is reported by macOS as an error (SMAppServiceErrorDomain 22,
// "Invalid argument") rather than as the no-op it is.
func Disable(label string) error {
	if err := checkLabel(label); err != nil {
		return err
	}
	if _, ok := bundled(); ok {
		name := plistName(label)
		st, err := smStatusOf(name)
		if err != nil {
			return fmt.Errorf("launchagent: %w", err)
		}
		if st.Registered() {
			if err := smUnregist(name); err != nil {
				return fmt.Errorf("launchagent: %w", err)
			}
		}
	}
	return Remove(label)
}
