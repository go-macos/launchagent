# go-macos/launchagent

**The per-user LaunchAgent that keeps a program running across logouts and
restarts.** Pure Go, `CGO_ENABLED=0`, no AppKit — path and file work only, so
it builds and is tested on every platform, which is what lets a
cross-compiling build write a macOS agent.

```go
path, err := launchagent.Install(launchagent.Spec{
    Label:      "io.github.go-downloader.godl",
    Program:    "/usr/local/bin/godl",
    Args:       []string{"queue", "run"},
    RunAtLoad:  true,          // start it at login — this is what survives a restart
    KeepAlive:  true,          // start it again when it exits
    StdoutPath: "/tmp/godl.log",
})

ok, err := launchagent.Installed("io.github.go-downloader.godl")
err = launchagent.Remove("io.github.go-downloader.godl")
```

## Why

A program started in a terminal ends with the terminal, and a program that ends
is a queue that stops, a watcher that stops watching, a sync that silently
falls behind. launchd is how macOS is told to start something again — and the
whole of that instruction is a plist in one directory.

## Notes

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

BSD-3-Clause.
