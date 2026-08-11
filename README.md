# Hopper

A simple TUI for selecting an SSH host from your `~/.ssh/config` and connecting to it. It uses a fuzzy finder to make it easy to find the host you're looking for.

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

Run `hopper` in your terminal:

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

## Configuration

`hopper` uses your existing `~/.ssh/config` file. No additional configuration is needed. It will pick up hosts, usernames, ports, and identity files from your SSH config.

`hopper` will recursively follow `Include` statements in your ssh config files.

## Building from source

To build from source, you'll need Go installed.

```sh
git clone https://github.com/kejrak/hopper.git
cd hopper
go build
./hopper
```

You can also use the included `makefile`:

```sh
git clone https://github.com/kejrak/hopper.git
cd hopper
make
./hopper
```
