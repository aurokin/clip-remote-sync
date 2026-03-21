# clip-remote-sync

`crs` syncs clipboard content from a configured source machine to the local machine.

The scope is intentionally narrow: one command, one destination machine, and predictable clipboard behavior for text and images.

## Usage

```bash
crs <source>
```

Examples:

```bash
crs luma
crs haste
```

## Behavior Contract

`crs <source>` always runs on the destination machine.

Text flow:

1. `crs` captures text from the source clipboard.
2. Trailing line endings are normalized.
3. The destination clipboard is updated to that text.

Image flow:

1. `crs` captures image bytes from the source clipboard.
2. The image is written locally on the destination at `/tmp/clip/<generated>.png`.
3. The destination clipboard is updated to the image itself.
4. The source clipboard is updated to the destination-local `/tmp/clip/<generated>.png` path as text.

This matches the remote-workflow case where you are operating `bront` from another machine and want:

- the actual image available on `bront`
- a pasteable `/tmp/clip/...png` path back on the source machine

## Codex Note

Some clients may treat a pasted local image path as an image attachment instead of plain text. That is upstream client behavior, not `crs` changing the clipboard contract.

In particular, if the source clipboard contains `/tmp/clip/...png` as text, Codex may still resolve that path into an image attachment when pasted.

## Launch Modes

There are two source launch modes:

- `direct`: the destination SSHes to the source and runs `crs _capture` / `crs _set-clipboard-text` directly
- `task`: the destination SSHes to the source only to trigger scheduled tasks, and those tasks launch `crs.exe` inside the interactive user session

Windows should generally use `launch_mode: "task"` because clipboard access is often only available from the interactive desktop session.

## Source Requirements

General requirements:

- SSH access to the source machine as the user that owns the clipboard session
- `crs` installed in `PATH`, or `remote_bin` configured to a full path

macOS source requirements:

- `pbpaste`
- `pbcopy`
- `osascript`
- `sips`

These are standard on macOS, so there is usually no extra setup beyond SSH access.

Linux source requirements:

- For reading image clipboard data: `wl-paste` or `xclip`
- For reading text clipboard data: `wl-paste`, `xclip`, or `xsel`
- For writing the returned text path back into the clipboard: `wl-copy`, `xclip`, or `xsel`

If your Linux source is Wayland-based, the expected pair is `wl-paste` and `wl-copy`.

Windows source requirements:

- SSH access into the Windows machine
- `crs.exe` installed and reachable over SSH
- PowerShell available for clipboard access
- Two scheduled tasks that run in the interactive user session

Recommended task actions:

- capture task: `crs.exe _capture-task-runner C:\ProgramData\clip-remote-sync`
- set-text task: `crs.exe _set-clipboard-text-task-runner C:\ProgramData\clip-remote-sync`

In task mode, `crs` writes request-scoped files under `requests\` and waits for matching result files under `results\`. That keeps concurrent runs isolated while still using Task Scheduler as the session bridge.

## Destination Requirements

The destination machine must be able to write to its own clipboard.

Linux destination requirements:

- For writing text: `wl-copy`, `xclip`, or `xsel`
- For writing images: `wl-copy` or `xclip`

Current local image clipboard support is implemented for Linux destinations.

## Config

Host information stays out of git. Copy `config.example.json` to your local config path:

```bash
mkdir -p ~/.config/clip-remote-sync
cp config.example.json ~/.config/clip-remote-sync/config.json
```

Or point `crs` at another file:

```bash
CRS_CONFIG=/path/to/config.json crs <source>
crs --config /path/to/config.json <source>
```

Config shape:

```json
{
  "destination": {
    "name": "primary-destination"
  },
  "sources": {
    "mac_source": {
      "ssh_target": "user@mac-source.example",
      "launch_mode": "direct",
      "remote_bin": "crs"
    },
    "windows_source": {
      "ssh_target": "user@windows-source.example",
      "launch_mode": "task",
      "remote_bin": "C:\\Program Files\\clip-remote-sync\\crs.exe",
      "task_bridge_dir": "C:\\ProgramData\\clip-remote-sync",
      "capture_task_name": "crs_capture",
      "set_text_task_name": "crs_set_clipboard_text"
    }
  }
}
```

## Build

```bash
go build -o bin/crs ./cmd/crs
GOOS=windows GOARCH=amd64 go build -o bin/crs-windows-amd64.exe ./cmd/crs
```

## Development

Install tooling:

```bash
make tools
```

Run the full local quality gate:

```bash
make check
```

That runs:

- `go fmt`
- `go vet`
- `go test`
- `go test -race`
- `golangci-lint`
- Linux and Windows builds

## CI

GitHub Actions are currently manual-only via `workflow_dispatch` while the workflows are still being developed.

Available manual workflows:

- `CI`: runs tests, race tests, vet, lint, and platform builds
- `Release`: builds `crs`, `crs-windows-amd64.exe`, and `SHA256SUMS`, then publishes a GitHub release

## Install

Install from source with Go:

```bash
go install github.com/aurokin/clip-remote-sync/cmd/crs@latest
```

Install a released Linux binary:

```bash
version=v0.1.0
base=https://github.com/aurokin/clip-remote-sync/releases/download/$version
curl -fsSLO "$base/crs"
curl -fsSLO "$base/SHA256SUMS"
sha256sum -c --ignore-missing SHA256SUMS
install -m 0755 crs ~/.local/bin/crs
```

Install a released Windows binary:

```powershell
$version = 'v0.1.0'
$base = "https://github.com/aurokin/clip-remote-sync/releases/download/$version"
Invoke-WebRequest "$base/crs-windows-amd64.exe" -OutFile .\crs-windows-amd64.exe
Invoke-WebRequest "$base/SHA256SUMS" -OutFile .\SHA256SUMS
Get-FileHash .\crs-windows-amd64.exe -Algorithm SHA256
```

Compare the Windows SHA-256 output with the matching `crs-windows-amd64.exe` line in `SHA256SUMS`.

## Mise

This project is Go-based, so it works cleanly with an existing `mise` Go toolchain. For local development:

```bash
mise x -- go build -o bin/crs ./cmd/crs
```

For public distribution, the clean path is GitHub releases with prebuilt binaries. `mise` can then install from a release backend such as `ubi`.
