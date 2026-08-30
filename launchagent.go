// Copyright (c) 2026, the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause license that can be
// found in the LICENSE file.

// Package launchagent is the per-user LaunchAgent that keeps a program running
// across logouts and restarts.
//
// A program a person started in a terminal ends with the terminal, and a
// program that ends is a queue that stops, a watcher that stops watching, a
// sync that silently falls behind. launchd is how macOS is told to start
// something again — and the whole of that instruction is a plist in one
// directory, so this is path and file work: no cgo, no AppKit, and testable on
// every platform, which is what lets a cross-compiling build write one.
package launchagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Spec is the program to keep running.
type Spec struct {
	// Label identifies the agent to launchd, in reverse-DNS form. It is also
	// the plist's file name, and what Remove and Installed are given.
	Label string
	// Program is the executable, and Args what follows it. The program is
	// named absolutely: launchd starts it from no particular directory and
	// with no particular PATH.
	Program string
	Args    []string
	// WorkingDir is where the program runs. Empty leaves launchd's choice.
	WorkingDir string
	// RunAtLoad starts it when the user logs in, which is what makes this
	// survive a restart rather than merely a terminal closing.
	RunAtLoad bool
	// KeepAlive starts it again when it exits. A program that means to
	// finish should leave this false, or launchd will fight it.
	KeepAlive bool
	// StdoutPath and StderrPath are where its output goes. A service with
	// nowhere to write is a service nobody can diagnose.
	StdoutPath string
	StderrPath string
}

// Seams: the failures below belong to a machine, not to a path, and none can
// be arranged on a working one.
var (
	homeDir   = os.UserHomeDir
	mkdirAll  = os.MkdirAll
	writeFile = os.WriteFile
	remove    = os.Remove
	stat      = os.Stat
)

// Dir is where a user's own agents live.
func Dir() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("launchagent: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// Path is where the agent with this label is kept, whether or not it is there.
func Path(label string) (string, error) {
	if err := checkLabel(label); err != nil {
		return "", err
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, label+".plist"), nil
}

// checkLabel refuses what cannot be a file name.
//
// A label reaches the filesystem as a path, so one carrying a separator would
// write the agent somewhere nobody asked for — and launchd would never find it
// there, which is a service that silently never runs.
func checkLabel(label string) error {
	switch {
	case label == "":
		return errors.New("launchagent: an agent with no label")
	case strings.ContainsAny(label, `/\`), label == "." || label == "..":
		return fmt.Errorf("launchagent: %q cannot be a file name", label)
	}
	return nil
}

// Install writes the agent and reports where it put it.
//
// It does not ask launchd to load it: that is a running session's business,
// and a build or an installer has no session to speak for. RunAtLoad covers
// the next login, which is the case this exists for.
func Install(s Spec) (string, error) {
	if err := checkLabel(s.Label); err != nil {
		return "", err
	}
	if s.Program == "" {
		return "", errors.New("launchagent: an agent with nothing to run")
	}
	path, err := Path(s.Label)
	if err != nil {
		return "", err
	}
	if err := mkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("launchagent: create %s: %w", filepath.Dir(path), err)
	}
	if err := writeFile(path, []byte(s.plist()), 0o644); err != nil {
		return "", fmt.Errorf("launchagent: write %s: %w", path, err)
	}
	return path, nil
}

// Remove takes the agent away. An agent that is not there is not an error:
// asking for it to be gone and finding it gone is the outcome wanted.
func Remove(label string) error {
	path, err := Path(label)
	if err != nil {
		return err
	}
	if err := remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("launchagent: remove %s: %w", path, err)
	}
	return nil
}

// Installed reports whether the agent is there.
func Installed(label string) (bool, error) {
	path, err := Path(label)
	if err != nil {
		return false, err
	}
	if _, err := stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("launchagent: %s: %w", path, err)
	}
	return true, nil
}

// plist is what launchd reads.
func (s Spec) plist() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	str := func(k, v string) { fmt.Fprintf(&b, "\t<key>%s</key>\n\t<string>%s</string>\n", k, escape(v)) }
	boolean := func(k string, v bool) {
		if v {
			fmt.Fprintf(&b, "\t<key>%s</key>\n\t<true/>\n", k)
		}
	}
	str("Label", s.Label)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range append([]string{s.Program}, s.Args...) {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", escape(a))
	}
	b.WriteString("\t</array>\n")
	if s.WorkingDir != "" {
		str("WorkingDirectory", s.WorkingDir)
	}
	if s.StdoutPath != "" {
		str("StandardOutPath", s.StdoutPath)
	}
	if s.StderrPath != "" {
		str("StandardErrorPath", s.StderrPath)
	}
	boolean("RunAtLoad", s.RunAtLoad)
	boolean("KeepAlive", s.KeepAlive)
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// escape keeps a path with an ampersand in it from ending the document early.
// A plist is XML, and a file called "rock & roll" is a file somebody has.
func escape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
