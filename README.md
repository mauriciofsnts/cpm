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
cd ~/dev/company/repo && cpm use work          # this project (and subdirs) uses "work"
cpm use work --dir ~/dev/company               # or a whole directory tree
cd ~/dev/oss/thing && cpm use personal --local # or pin via ./.claude-profile inside the repo
```

`cpm use` keeps the per-project choice in `~/.config/cpm/dirs`, so nothing is
written into the project itself. Use `--local` when you want the pin to travel
with the repo instead.

The first `claude` in a fresh profile asks you to log in. That login stays in
that profile only.

## Resolution order

1. `$CPM_PROFILE`
2. `.claude-profile` in the cwd or any parent (only if you opted in with `--local`)
3. directory rules in `~/.config/cpm/dirs` (longest prefix wins; where `cpm use` writes)
4. `~/.config/cpm/default`

`cpm which` tells you which one won and why.

## Commands

| command | what it does |
|---|---|
| `cpm` | list profiles, `*` = active here, `(default)` = global fallback |
| `cpm add <name> [dir] [--from <p>]` | register a profile (creates dir if missing) |
| `cpm rm <name>` | unregister, never deletes the config dir |
| `cpm use <name> [--dir <path>]` | pin for this project (or another tree), stored in `~/.config/cpm/dirs` |
| `cpm use <name> --local` | pin by writing `./.claude-profile` into the project |
| `cpm use <name> --global` | set the default profile |
| `cpm which` | resolved profile for the cwd and its source |
| `cpm dir [name]` | print a profile's config dir |
| `cpm run [args]` | exec `claude` with the right `CLAUDE_CONFIG_DIR` |
| `cpm shell [zsh\|bash\|fish]` | print the `claude` wrapper function |

State lives in `~/.config/cpm/`: `profiles/<name>` symlinks, `default`, `dirs`.
No config format to learn, edit it with `ls`, `ln` and `echo`.
