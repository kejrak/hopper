# Hopper

A simple TUI for selecting an SSH host from your `~/.ssh/config` and connecting to it. Its built-in fuzzy-filtered list makes it easy to find the host you're looking for.

## Features

-   Parses your `~/.ssh/config` file, including `Include` directives — hosts from all config files appear, grouped by the file they live in.
-   Two-pane TUI: fuzzy-filtered host list with a RECENT section, plus a detail panel (user, hostname, port, identity file, agent status, source file, last connected).
-   Connects with plain `ssh <host>` — OpenSSH resolves users, ports, keys, and ProxyJump itself, and hopper mirrors ssh's exit code.
-   `ctrl+a` loads the highlighted host's key into your ssh-agent (`ssh-add`, passphrase prompted by ssh-add itself).
-   `ctrl+e` opens the host's config file in `$EDITOR` and reloads the list afterwards.
-   Cross-platform (macOS, Linux, Windows).

## Installation

You can install `hopper` using `go install`:

```sh
go install github.com/kejrak/hopper@latest
```

## Usage

Run `hopper` in your terminal for the interactive picker:

```sh
hopper
```

Type to fuzzy-filter, `↑`/`↓` to move, then:

| Key | Action |
|---|---|
| `enter` | Connect to the highlighted host |
| `ctrl+a` | Add the host's key to the ssh-agent |
| `ctrl+e` | Edit the host's config file in `$EDITOR` |
| `esc` / `ctrl+c` | Quit |

Connection history is stored in a small local state file (`~/.local/state/hopper/history.json` on Linux, platform equivalent elsewhere) to power the RECENT section — delete it any time.

## Scripting and AI agents

AI coding agents (Claude Code, Codex, …) and scripts usually run without a terminal, so they can't drive the TUI. Use the non-interactive subcommands instead:

```sh
hopper list --json               # every host as JSON (name, user, hostname, port, identity_file, source, group, last_connected)
hopper show web-prod --json      # one host
hopper exec web-prod -- uptime   # run a command, no prompts
```

`hopper exec` only accepts hosts defined in your ssh config and runs `ssh -o BatchMode=yes -T -- <host> <cmd>`, so it fails fast instead of waiting for a password or passphrase (load keys into your ssh-agent first, e.g. with `ctrl+a` in the TUI). It exits with the remote command's exit code (255 when ssh itself fails) and is recorded in RECENT like a normal connection. Errors and warnings go to stderr; stdout carries only the output.

Like plain `ssh`, `hopper exec` forwards its stdin to the remote command, so `echo data | hopper exec web-prod -- tee /tmp/f` works. In shell loops, or anywhere stdin may stay open, add `< /dev/null` so ssh doesn't consume the loop's input or wait for data: `hopper exec "$h" -- uptime < /dev/null`.

Exit codes: `0` success, `1` error (unknown host, unreadable config), `2` usage error, otherwise the remote/ssh exit code. Running bare `hopper` without a terminal exits `1` with a pointer to these commands.

To let an agent use hopper, add a line like this to your `CLAUDE.md` / `AGENTS.md`:

> SSH hosts: run `hopper list --json` to discover hosts and `hopper exec <host> -- <cmd>` to run commands on them.

## Configuration

`hopper` uses your existing `~/.ssh/config` file. No additional configuration is needed. It will pick up hosts, usernames, ports, and identity files from your SSH config.

`hopper` will recursively follow `Include` statements in your ssh config files.

## Building from source

To build from source, you'll need Go installed.

```sh
git clone https://github.com/kejrak/hopper.git
cd hopper
go build -o bin/hopper .
./bin/hopper
```

You can also use the included `makefile`, which builds the same binary to `bin/hopper`:

```sh
git clone https://github.com/kejrak/hopper.git
cd hopper
make build
./bin/hopper
```
