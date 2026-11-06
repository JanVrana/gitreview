# gitreview

[![Release](https://img.shields.io/github/v/release/JanVrana/gitreview)](https://github.com/JanVrana/gitreview/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/JanVrana/gitreview)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)
![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macOS-blue)
![Dependencies](https://img.shields.io/badge/dependencies-none-brightgreen)

**English** · [Čeština](README.cs.md)

A web UI for reviewing the changes of your current git branch against `develop` / `master`, with an IntelliJ-style side-by-side diff (original on the left, changes on the right).

It was built for reviewing the work of AI coding agents such as Claude Code running on a remote VM, where desktop diff tools are not at hand. Open the page from your own browser, leave comments on lines, and hand them back to the agent as a `REVIEW.md` file.

gitreview is a single Go binary with no dependencies besides `git`. The frontend is embedded and works offline, with no CDN.

![Side-by-side diff in the dark theme with a review comment](docs/screenshots/diff-dark.png)

<table>
<tr>
<td><img src="docs/screenshots/word-diff.png" alt="Word-level highlighting"><br><sub>Changed words highlighted inside lines</sub></td>
<td><img src="docs/screenshots/diff-light.png" alt="Light theme"><br><sub>Light theme with folded unchanged regions</sub></td>
<td><img src="docs/screenshots/comments-panel.png" alt="Comments panel"><br><sub>Comments panel and REVIEW.md export</sub></td>
</tr>
</table>

## Install

Prebuilt binary for Linux / macOS (amd64, arm64) into `~/.local/bin`:

```bash
mkdir -p ~/.local/bin && curl -fL -o ~/.local/bin/gitreview \
  "https://github.com/JanVrana/gitreview/releases/latest/download/gitreview-$(uname -s | tr A-Z a-z)-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" \
  && chmod +x ~/.local/bin/gitreview && gitreview -version
```

If `~/.local/bin` is not on your `PATH`, put the binary somewhere else, for example `/usr/local/bin` with `sudo`. Every release includes a `SHA256SUMS` file.

With Go 1.26 or newer:

```bash
go install github.com/JanVrana/gitreview@latest
```

From source: `git clone https://github.com/JanVrana/gitreview && cd gitreview && make build`.

To update, run the same command again.

## Usage

```bash
gitreview /path/to/repo          # defaults: current directory, port 8765, 0.0.0.0
gitreview -p 9000 -b 127.0.0.1 .
```

On startup it prints the URLs it is reachable on (the machine's IP addresses), each with an access token, e.g. `http://192.168.1.20:8765/?token=3f9c…`. Open one of them in your browser. The browser keeps the token in a cookie, so later visits to the plain address work too.

| Flag | Meaning |
|---|---|
| `-p` | port (default `8765`) |
| `-b` | listen address (default `0.0.0.0`) |
| `-allow-host` | extra host names accepted in the `Host` header, comma separated |
| `-token` | fixed access token instead of a random one (also `GITREVIEW_TOKEN`); keeps bookmarks working across restarts |
| `-no-auth` | no access token: anyone who can reach the port can use the UI |
| `-version` | print the version and exit |

### Review loop with an AI agent

1. Let the agent work on a branch.
2. Open gitreview, go through the changed files and click a line number to leave a comment.
3. Click **Export REVIEW.md**. This writes all comments with code snippets to `REVIEW.md` in the repository root. **Copy** copies the same text to the clipboard instead.
4. Tell the agent: *"address the comments in REVIEW.md"*.
5. The page refreshes by itself as the agent edits files. Comments whose line has changed are marked as stale.

## Security

Every request needs the **access token**. By default a random token is generated at each start and printed in the URLs; set a fixed one with `-token` or `GITREVIEW_TOKEN` (prefer the variable, command-line arguments are visible in `ps`). The token is accepted as `?token=` (exchanged for an HttpOnly cookie and removed from the address bar), as the cookie, or as `Authorization: Bearer <token>` for scripts.

`-no-auth` turns the token off, for example on a private VM or behind an SSH tunnel (`-b 127.0.0.1` + `ssh -L 8765:localhost:8765 vm`). The server runs over plain http, so on an untrusted network the token can be sniffed; use the tunnel there.

Further protections, also active with `-no-auth`:

- Writes (comments, export) are accepted only as same-origin JSON requests, so a foreign web page open in your browser cannot inject text into `REVIEW.md`.
- To prevent DNS rebinding, the `Host` header must be an IP address, `localhost` or the machine's hostname. Allow other names with `-allow-host vm.example.com,dev-vm`.
- Only files from the list of changed files can be read.
- The server is read-only for the repository. It writes only `REVIEW.md` and `.git/gitreview/comments.json`.

## Features

- **Comparison:**
  - base `develop` / `master` / `main` / any branch (falls back to `origin/<branch>`),
  - left side: *merge-base* (like a pull request) or the *tip of the base branch*,
  - right side: *working tree* (including uncommitted and untracked files) or *HEAD*,
  - an *ignore whitespace* toggle.
- **File tree:** IntelliJ-style status colors, +/− line counts and a "reviewed" checkbox that clears itself when the file changes again.
- **Diff:**
  - aligned change blocks with similar lines paired, changed words highlighted and syntax highlighting (highlight.js),
  - folded unchanged regions,
  - a change overview ruler on the right,
  - independent horizontal scrolling of each side.
- **Auto-refresh:** when files change, a commit is made or the branch is switched, the UI reloads by itself (polling about every 1.5 s, pushed over SSE).
- **Review comments:**
  - stored in `.git/gitreview/comments.json`,
  - `REVIEW.md` is added to `.git/info/exclude` automatically, so it never gets committed,
  - copying to the clipboard works over plain http too.
- **Settings (⚙):** dark (Darcula) or light theme, English or Czech UI. The language defaults to your browser's; the exported `REVIEW.md` follows the UI language.

## Key bindings

| Key | Action |
|---|---|
| F7 / Shift+F7 | next / previous change (continues into the next file) |
| Alt+↓ / Alt+↑ | next / previous file |
| click a line number | add a comment |
| Ctrl+Enter / Esc | save / cancel a comment |
| Shift+wheel | scroll the side under the cursor horizontally |

## Development

```bash
make test   # go test ./...
make vet
make dist   # release binaries in dist/ (gitreview-<os>-<arch> + SHA256SUMS)
```

Layout:
- `internal/git`: git CLI calls (`--no-optional-locks`, no shell, ref and path validation)
- `internal/diff`: turns `git diff -U0` into aligned blocks
- `internal/review`: comments and export
- `internal/lang`: file-to-language mapping (syntax highlighting and export)
- `internal/server`: HTTP API, CSRF and DNS-rebinding protection, shared SSE watcher
- `web/`: vanilla JS frontend

## License

[MIT](LICENSE). The bundled [highlight.js](https://highlightjs.org/) (`web/vendor/`) is licensed under BSD-3-Clause.
