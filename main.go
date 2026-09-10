// cpm — Claude profile manager.
//
// A profile is just a Claude config directory (what CLAUDE_CONFIG_DIR points
// to). Profiles are registered as entries under ~/.config/cpm/profiles/, each
// one a symlink (or a real directory) named after the profile.
//
// Resolution order for the active profile:
//  1. $CPM_PROFILE
//  2. a .claude-profile file in the cwd or any parent directory
//  3. a directory rule in ~/.config/cpm/dirs (longest matching prefix wins)
//  4. the default profile in ~/.config/cpm/default
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const (
	profileFile = ".claude-profile"
	envProfile  = "CPM_PROFILE"
	envBin      = "CPM_CLAUDE_BIN"
)

// Files copied when creating a profile with --from.
var seedFiles = []string{"settings.json", "keybindings.json", "CLAUDE.md"}
var seedDirs = []string{"skills", "agents", "commands"}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cpm:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return list()
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "ls", "list":
		return list()
	case "add":
		return add(rest)
	case "rm", "remove":
		return remove(rest)
	case "use":
		return use(rest)
	case "which", "current":
		return which()
	case "dir":
		return dir(rest)
	case "run", "exec":
		return execClaude(rest)
	case "shell":
		return shell(rest)
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `cpm — switch Claude Code accounts per project

usage:
  cpm                          list profiles (* = active here, (default) = global default)
  cpm add <name> [dir]         register a profile; creates dir if missing, ~/.config/cpm/profiles/<name> if omitted
      --from <profile>         seed settings.json, keybindings, CLAUDE.md, skills/, agents/, commands/ from another profile
  cpm rm <name>                unregister a profile (never deletes the config dir)
  cpm use <name>               pin profile for this project (writes ./.claude-profile)
  cpm use <name> --dir [path]  pin profile for a directory tree (default: cwd), stored in ~/.config/cpm/dirs
  cpm use <name> --global      set the default profile
  cpm which                    show the profile resolved for the cwd and where it came from
  cpm dir [name]               print the config dir of a profile (default: active one)
  cpm run [claude args...]     exec claude with CLAUDE_CONFIG_DIR set to the active profile
  cpm shell [zsh|bash|fish]    print a 'claude' shell function that goes through 'cpm run'

env:
  CPM_PROFILE      force a profile
  CPM_CLAUDE_BIN   path to the real claude binary (default: first 'claude' in PATH)
`)
}

// ---------- paths ----------

func configHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "cpm")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "cpm")
}

func profilesDir() string { return filepath.Join(configHome(), "profiles") }
func defaultFile() string { return filepath.Join(configHome(), "default") }
func dirsFile() string    { return filepath.Join(configHome(), "dirs") }

func profilePath(name string) string { return filepath.Join(profilesDir(), name) }

func validName(name string) error {
	if name == "" || strings.ContainsAny(name, "/\\ \t\n") || name == "." || name == ".." {
		return fmt.Errorf("invalid profile name %q", name)
	}
	return nil
}

// profileDir returns the resolved config directory for a registered profile.
func profileDir(name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	p := profilePath(name)
	if _, err := os.Lstat(p); err != nil {
		return "", fmt.Errorf("profile %q not found (run 'cpm add %s <dir>')", name, name)
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("profile %q: %w", name, err)
	}
	return real, nil
}

func profileNames() ([]string, error) {
	entries, err := os.ReadDir(profilesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ---------- resolution ----------

type resolved struct {
	name   string
	source string // human-readable origin
}

func resolve() (resolved, error) {
	if n := os.Getenv(envProfile); n != "" {
		return resolved{n, "$" + envProfile}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return resolved{}, err
	}
	for d := cwd; ; d = filepath.Dir(d) {
		f := filepath.Join(d, profileFile)
		if n := readTrim(f); n != "" {
			return resolved{n, f}, nil
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	if n, path := matchDirRule(cwd); n != "" {
		return resolved{n, "rule " + path}, nil
	}
	if n := readTrim(defaultFile()); n != "" {
		return resolved{n, "default"}, nil
	}
	return resolved{}, errors.New("no profile for this directory and no default set (run 'cpm use <name> --global')")
}

type dirRule struct{ name, path string }

func readDirRules() []dirRule {
	f, err := os.Open(dirsFile())
	if err != nil {
		return nil
	}
	defer f.Close()
	var rules []dirRule
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, path, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		rules = append(rules, dirRule{name, strings.TrimSpace(path)})
	}
	return rules
}

func writeDirRules(rules []dirRule) error {
	var b strings.Builder
	for _, r := range rules {
		fmt.Fprintf(&b, "%s %s\n", r.name, r.path)
	}
	if err := os.MkdirAll(configHome(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dirsFile(), []byte(b.String()), 0o644)
}

func matchDirRule(cwd string) (name, path string) {
	best := -1
	for _, r := range readDirRules() {
		if cwd == r.path || strings.HasPrefix(cwd, strings.TrimSuffix(r.path, "/")+"/") {
			if len(r.path) > best {
				best, name, path = len(r.path), r.name, r.path
			}
		}
	}
	return name, path
}

// ---------- commands ----------

// accountEmail reads the logged-in account from <dir>/.claude.json, if any.
func accountEmail(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		return ""
	}
	var v struct {
		OauthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	return v.OauthAccount.EmailAddress
}

func list() error {
	names, err := profileNames()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("no profiles yet. try:  cpm add work ~/.claude")
		return nil
	}
	active, _ := resolve()
	def := readTrim(defaultFile())
	w := 0
	for _, n := range names {
		w = max(w, len(n))
	}
	for _, n := range names {
		mark := " "
		if n == active.name {
			mark = "*"
		}
		d, err := profileDir(n)
		if err != nil {
			d = "(broken link)"
		}
		line := fmt.Sprintf("%s %-*s  %s", mark, w, n, d)
		if e := accountEmail(d); e != "" {
			line += "  " + e
		}
		if n == def {
			line += "  (default)"
		}
		fmt.Println(line)
	}
	return nil
}

func add(args []string) error {
	var from string
	var pos []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			if i+1 >= len(args) {
				return errors.New("--from needs a profile name")
			}
			from = args[i+1]
			i++
		default:
			pos = append(pos, args[i])
		}
	}
	if len(pos) == 0 || len(pos) > 2 {
		return errors.New("usage: cpm add <name> [dir] [--from <profile>]")
	}
	name := pos[0]
	if err := validName(name); err != nil {
		return err
	}
	if _, err := os.Lstat(profilePath(name)); err == nil {
		return fmt.Errorf("profile %q already exists", name)
	}
	if err := os.MkdirAll(profilesDir(), 0o755); err != nil {
		return err
	}

	var fromDir string
	if from != "" {
		d, err := profileDir(from)
		if err != nil {
			return err
		}
		fromDir = d
	}

	target := profilePath(name)
	if len(pos) == 2 {
		abs, err := filepath.Abs(expandHome(pos[1]))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return err
		}
		if err := os.Symlink(abs, target); err != nil {
			return err
		}
		target = abs
	} else if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}

	if fromDir != "" {
		if err := seed(fromDir, target); err != nil {
			return err
		}
	}
	if readTrim(defaultFile()) == "" {
		if err := os.WriteFile(defaultFile(), []byte(name+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Printf("set %q as default profile\n", name)
	}
	fmt.Printf("added %s -> %s\n", name, target)
	if accountEmail(target) == "" {
		fmt.Println("not logged in yet: run 'cpm run /login' or 'CPM_PROFILE=" + name + " claude' to authenticate")
	}
	return nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[1:])
	}
	return p
}

func seed(src, dst string) error {
	for _, f := range seedFiles {
		if err := copyFile(filepath.Join(src, f), filepath.Join(dst, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, d := range seedDirs {
		if err := os.CopyFS(filepath.Join(dst, d), os.DirFS(filepath.Join(src, d))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func remove(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: cpm rm <name>")
	}
	name := args[0]
	if err := validName(name); err != nil {
		return err
	}
	p := profilePath(name)
	st, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("profile %q not found", name)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("%q is a real directory at %s; delete it yourself if you really want to lose that login", name, p)
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	if readTrim(defaultFile()) == name {
		os.Remove(defaultFile())
		fmt.Println("default profile cleared")
	}
	fmt.Printf("removed %s (config dir left untouched)\n", name)
	return nil
}

func use(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cpm use <name> [--global | --dir [path]]")
	}
	name := args[0]
	if _, err := profileDir(name); err != nil {
		return err
	}
	rest := args[1:]
	switch {
	case len(rest) == 0:
		if err := os.WriteFile(profileFile, []byte(name+"\n"), 0o644); err != nil {
			return err
		}
		cwd, _ := os.Getwd()
		fmt.Printf("%s pinned to %q via %s\n", cwd, name, filepath.Join(cwd, profileFile))
	case rest[0] == "--global" && len(rest) == 1:
		if err := os.MkdirAll(configHome(), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(defaultFile(), []byte(name+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Printf("default profile is now %q\n", name)
	case rest[0] == "--dir" && len(rest) <= 2:
		p := "."
		if len(rest) == 2 {
			p = expandHome(rest[1])
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		rules := readDirRules()
		kept := rules[:0]
		for _, r := range rules {
			if r.path != abs {
				kept = append(kept, r)
			}
		}
		kept = append(kept, dirRule{name, abs})
		if err := writeDirRules(kept); err != nil {
			return err
		}
		fmt.Printf("%s/** -> %q\n", abs, name)
	default:
		return errors.New("usage: cpm use <name> [--global | --dir [path]]")
	}
	return nil
}

func which() error {
	r, err := resolve()
	if err != nil {
		return err
	}
	d, err := profileDir(r.name)
	if err != nil {
		return fmt.Errorf("%w (selected by %s)", err, r.source)
	}
	fmt.Printf("%s\t%s\t(%s)\n", r.name, d, r.source)
	return nil
}

func dir(args []string) error {
	var name string
	if len(args) > 0 {
		name = args[0]
	} else {
		r, err := resolve()
		if err != nil {
			return err
		}
		name = r.name
	}
	d, err := profileDir(name)
	if err != nil {
		return err
	}
	fmt.Println(d)
	return nil
}

func execClaude(args []string) error {
	r, err := resolve()
	if err != nil {
		return err
	}
	d, err := profileDir(r.name)
	if err != nil {
		return fmt.Errorf("%w (selected by %s)", err, r.source)
	}
	bin := os.Getenv(envBin)
	if bin == "" {
		bin, err = exec.LookPath("claude")
		if err != nil {
			return errors.New("claude binary not found in PATH (set " + envBin + ")")
		}
	}
	env := append(os.Environ(), "CLAUDE_CONFIG_DIR="+d)
	return syscall.Exec(bin, append([]string{"claude"}, args...), env)
}

func shell(args []string) error {
	sh := "zsh"
	if len(args) > 0 {
		sh = args[0]
	}
	switch sh {
	case "zsh", "bash":
		fmt.Print("claude() { cpm run \"$@\"; }\n")
	case "fish":
		fmt.Print("function claude; cpm run $argv; end\n")
	default:
		return fmt.Errorf("unsupported shell %q", sh)
	}
	return nil
}
