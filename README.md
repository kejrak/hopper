# Hopper

A simple TUI for selecting an SSH host from your `~/.ssh/config` and connecting to it. It uses a fuzzy finder to make it easy to find the host you're looking for.

## Features

-   Parses your `~/.ssh/config` file, including `Include` directives.
-   Provides a fuzzy-findable list of hosts.
-   Connects to the selected host using your system's `ssh` command.
-   Cross-platform (macOS, Linux, Windows).

## Installation

You can install `hopper` using `go install`:

```sh
go install github.com/kejrak/hopper@latest
```

## Usage

Simply run `hopper` in your terminal:

```sh
hopper
```

This will open a fuzzy finder with a list of your SSH hosts. Start typing to filter the list.
Select a host and press Enter to connect.
Press `Ctrl+C` or `Esc` to exit without connecting.

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
