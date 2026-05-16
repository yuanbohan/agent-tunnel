# tunnel

Install the latest release:

```sh
curl -fsSL https://raw.githubusercontent.com/yuanbohan/tunnel/main/install.sh | sh
```

Install a specific version:

```sh
curl -fsSL https://raw.githubusercontent.com/yuanbohan/tunnel/main/install.sh | VERSION=v0.1.2 sh
```

Supported targets:

- macOS Apple Silicon (`darwin/arm64`)
- macOS Intel (`darwin/amd64`)
- Linux x86_64 (`linux/amd64`)
- Linux ARM64 (`linux/arm64`)

The installer writes `tunnel` to `~/.local/bin/tunnel`.

Manual lifecycle commands:

```sh
tunnel update
tunnel rollback
```

Interactive `tunnel run ...` also checks for updates at most once every 24 hours and may prompt before startup when a newer official release is available.

Verify the installed version:

```sh
tunnel --version
```
