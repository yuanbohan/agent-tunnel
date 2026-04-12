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

Verify the installed version:

```sh
tunnel --version
```

Compatibility:

- Tunnel and Relay are guaranteed compatible within the same compatibility line.
- For `v1+`, the compatibility line is the major version. `tunnel v1.4.2` is compatible with `relay v1.9.0`.
- For pre-`v1`, the compatibility line is `0.minor`. `tunnel v0.1.7` is compatible with `relay v0.1.3`, but not guaranteed with `relay v0.2.0`.
