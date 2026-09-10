# cpm

Switch Claude Code accounts per project. Zero dependencies, ~4 ms per call.

A profile is just a Claude config directory (what `CLAUDE_CONFIG_DIR` points to).
Each one keeps its own login, settings, plugins and history. `cpm` picks the
right one for the directory you are in and `exec`s the real `claude` binary.

## Install

```sh
go build -ldflags="-s -w" -o ~/.local/bin/cpm .
echo 'claude() { cpm run "$@"; }' >> ~/.zshrc   # or: eval "$(cpm shell)"
```

## Setup

```sh
cpm add personal ~/.claude                     # register an existing config dir
cpm add work ~/.claude-work --from personal    # new dir, copies settings/skills/agents
cpm use personal --global                      # fallback when nothing else matches
cd ~/dev/company && cpm use work --dir         # everything under this tree uses "work"
cd ~/dev/company/repo && cpm use work          # or pin one project via ./.claude-profile
```

The first `claude` in a fresh profile asks you to log in. That login stays in
that profile only.

## Resolution order

1. `$CPM_PROFILE`
2. `.claude-profile` in the cwd or any parent
3. directory rules in `~/.config/cpm/dirs` (longest prefix wins)
4. `~/.config/cpm/default`

`cpm which` tells you which one won and why.

## Commands

| command | what it does |
|---|---|
| `cpm` | list profiles, `*` = active here, `(default)` = global fallback |
| `cpm add <name> [dir] [--from <p>]` | register a profile (creates dir if missing) |
| `cpm rm <name>` | unregister, never deletes the config dir |
| `cpm use <name> [--global \| --dir [path]]` | pin for project, everywhere, or a directory tree |
| `cpm which` | resolved profile for the cwd and its source |
| `cpm dir [name]` | print a profile's config dir |
| `cpm run [args]` | exec `claude` with the right `CLAUDE_CONFIG_DIR` |
| `cpm shell [zsh\|bash\|fish]` | print the `claude` wrapper function |

State lives in `~/.config/cpm/`: `profiles/<name>` symlinks, `default`, `dirs`.
No config format to learn, edit it with `ls`, `ln` and `echo`.
