# Contributing

Thanks for helping improve DelayEngine.

## Project Rules

- Keep the core delay path packet-preserving.
- Do not recode live packets inside the delay pipeline.
- Keep MediaMTX compatibility.
- Keep the app modular and easy to run on Windows.
- Keep every change compiling with `go test ./...`.

## Development

```powershell
go test ./...
go run ./cmd/delayengine
```

Build the portable Windows folder:

```powershell
.\scripts\build-windows.ps1 -PortableClean
```

Generate a ZIP only when needed:

```powershell
.\scripts\build-windows.ps1 -PortableClean -Zip
```

## Pull Requests

Please describe:

- What changed.
- How you tested it.
- Whether it affects delay, Twitch relay, video conversion, or the web UI.

Avoid committing personal stream keys, local settings, generated logs, or converted loading videos.
