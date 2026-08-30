# go-macos/launchagent

[![ci](https://github.com/go-macos/launchagent/actions/workflows/ci.yml/badge.svg)](https://github.com/go-macos/launchagent/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-macos/launchagent.svg)](https://pkg.go.dev/github.com/go-macos/launchagent)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue.svg)](LICENSE)

**Keep a program running across logouts and restarts.** Pure Go,
`CGO_ENABLED=0`, and it builds and is tested on every platform, which is what
lets a cross-compiling build set up a macOS agent.

```go
reg, err := launchagent.Enable(launchagent.Spec{
    Label:      "io.github.go-downloader.godl",
    Program:    "/usr/local/bin/godl",
    Args:       []string{"queue", "run"},
    RunAtLoad:  true,          // start it at login — this is what survives a restart
    KeepAlive:  true,          // start it again when it exits
    StdoutPath: "/tmp/godl.log",
})
if err != nil {
    return err
}
if reg.Advice != "" {
    fmt.Println(reg.Advice)    // the person has to allow it; say so
}
```

## The two mechanisms, and which one you get

| | `MethodPlist` | `MethodAppService` |
|---|---|---|
| what it is | a plist in `~/Library/LaunchAgents` | `SMAppService`, via [`go-macos/servicemanagement`](https://github.com/go-macos/servicemanagement) |
| needs an application bundle | no | **yes** |
| macOS | any, and every other platform for the file work | 13+ |
| shown to the person as | a bare reverse-DNS label | the application, by name and icon |
| person can switch it off | not really | yes — and `State` reports it |
| supported by Apple | deprecated for applications | yes |

`Enable` prefers `SMAppService` **when this process is inside a bundle**, and
writes the plist otherwise. That is the whole rule.

## The plist API is unchanged

`Install`, `Remove`, `Installed`, `Path` and `Dir` still mean exactly what they
meant: the plist, and nothing else. A caller that wants the file — and knows it
wants the file — still gets it, with the same signatures and the same
behaviour.

| | |
|---|---|
| `Enable(Spec) (Registration, error)` | register by whichever mechanism applies, and say which. |
| `State(label) (Registration, error)` | what is actually set up, and how. |
| `Disable(label) error` | remove it — **both** ways, see below. |
| `Install(Spec) (string, error)` | write the plist. Unchanged. |
| `Remove(label) error` / `Installed(label) (bool, error)` | the plist alone. Unchanged. |
| `Path(label)` / `Dir()` | where the plist goes. Unchanged. |

## A refusal falls back, and says so

Inside a bundle whose `Contents/Library/LaunchAgents` does not hold
`LABEL.plist`, `SMAppService` refuses. `Enable` then writes the legacy plist and
puts the refusal in `Registration.Fallback`:

```go
if reg.Fallback != nil {
    log.Printf("SMAppService declined (%v); using %s", reg.Fallback, reg.Path)
}
```

Failing outright would stop a working program working the day it gained a
bundle. Falling back **silently** would hide a real packaging defect for as long
as anybody cared to look. So it falls back and it tells you, and a caller that
wants strictness has one field to check.

## `Registration.Advice` is not decoration

`SMAppService` can register a service and leave it **switched off**, waiting for
the person to allow it in System Settings → General → Login Items & Extensions.
That happens routinely — most of all when they turned this very item off before,
in which case it stays off whatever the program does.

`Enable` returns `nil` in that case and `Registration.Enabled` is `false`. A
program that ignores it starts nothing and says nothing; the `Advice` is the
sentence to put in front of the person, naming the pane they would never find on
their own.

## `Disable` takes away both

A program that shipped a plist and then gained a bundle has **two**
registrations. macOS acts on the `SMAppService` one, so removing only that
leaves the program still starting at login — the bug a person describes as
"I turned it off and it came back". `Disable` removes both, and finding nothing
to remove is not an error.

`SMAppService` is asked for its status before being asked to unregister, because
`-unregister:` on something that was never registered is reported by macOS as an
error (`SMAppServiceErrorDomain 22`, "Invalid argument") rather than as the no-op
it is.

## Notes on the plist

- `Install` writes the agent; it does **not** ask launchd to load it. That is a
  running session's business, and a build or an installer has no session to
  speak for. `RunAtLoad` covers the next login, which is the case this exists
  for.
- A label that cannot be a file name is refused. It reaches the filesystem as a
  path, so one carrying a separator would write the agent where launchd never
  looks — a service that silently never runs.
- Optional keys are written only when asked for. A plist naming a log nobody
  requested, or keeping alive a program meant to finish, is a service fighting
  its own author.
- A plist is XML, so a path is escaped: `/opt/rock & roll/godl` is a path
  somebody has, and unescaped it ends the document early.
- Removing an agent that is not there is not an error: asking for it to be gone
  and finding it gone is the outcome wanted.

## Building the bundle

`SMAppService` is only reachable from one.
[`go-macos/appbundle`](https://github.com/go-macos/appbundle) assembles it in
pure Go, in the same cross-compiling build:

```go
_, err := appbundle.Build(appbundle.Spec{
    Dir: "dist", Name: "godl", Identifier: "io.github.go-downloader.godl",
    Version: "0.1.0", Executable: "build/godl", Accessory: true,
})
```

The agent's plist goes at `Contents/Library/LaunchAgents/LABEL.plist` — the file
name `SMAppService` resolves, and nowhere else.

BSD-3-Clause.
