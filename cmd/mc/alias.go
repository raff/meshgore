package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/raff/meshgore"
)

// Aliases are names for command lines, kept in the user's config directory
// (mc/aliases), one "name = command; command" per line. A definition can chain
// commands with ";", and use $1..$9 and $* for the words typed after the alias.
// An alias never shadows a built-in command, and expands to built-in commands only
// (not to other aliases), so it cannot loop and every command keeps its own checks.

var aliasName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)

func aliasPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mc", "aliases"), nil
}

// loadAliases reads the aliases file; a missing or unreadable one gives none.
func loadAliases() map[string]string {
	out := map[string]string{}
	path, err := aliasPath()
	if err != nil {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, def, ok := strings.Cut(line, "=")
		name, def = strings.TrimSpace(name), strings.TrimSpace(def)
		if ok && aliasName.MatchString(name) && def != "" {
			out[name] = def
		}
	}
	return out
}

func saveAliases(aliases map[string]string) error {
	path, err := aliasPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, name := range sortedKeys(aliases) {
		fmt.Fprintf(&b, "%s = %s\n", name, aliases[name])
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// splitCommands cuts a definition into commands at the ";" outside quotes.
func splitCommands(def string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	for _, r := range def {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == ';':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, cur.String())
}

var placeholder = regexp.MustCompile(`\$([1-9]|\*)`)

// nodeVar is replaced by the name of the node mc is connected to, the one it
// advertises and that other nodes (and bots) call it by.
var nodeVar = regexp.MustCompile(`\$(nodename|node)`)

// expandAlias turns a definition and the words typed after the alias into the
// command lines to run. With no placeholder in the definition the words are added to
// the end of the last command; with placeholders, every word must be used. $node (or
// $nodename) is the node's own name; node is only called if a definition uses it.
func expandAlias(name, def string, args []string, node func() (string, error)) ([][]string, error) {
	var cmds [][]string
	for _, text := range splitCommands(def) {
		words, err := splitArgs(text)
		if err != nil {
			return nil, fmt.Errorf("alias %s: %w", name, err)
		}
		if len(words) > 0 {
			cmds = append(cmds, words)
		}
	}
	if len(cmds) == 0 {
		return nil, fmt.Errorf("alias %s is empty", name)
	}
	nodeName := ""
	for _, c := range cmds {
		for i, w := range c {
			if !nodeVar.MatchString(w) {
				continue
			}
			if nodeName == "" {
				n, err := node()
				if err != nil {
					return nil, fmt.Errorf("alias %s uses $node: %w", name, err)
				}
				nodeName = n
			}
			c[i] = nodeVar.ReplaceAllLiteralString(w, nodeName)
		}
	}
	need, star, any := 0, false, false
	for _, c := range cmds {
		for _, w := range c {
			for _, m := range placeholder.FindAllStringSubmatch(w, -1) {
				any = true
				if m[1] == "*" {
					star = true
				} else if n := int(m[1][0] - '0'); n > need {
					need = n
				}
			}
		}
	}
	if !any {
		last := len(cmds) - 1
		cmds[last] = append(cmds[last], args...)
		return cmds, nil
	}
	if len(args) < need {
		return nil, fmt.Errorf("alias %s needs %d argument(s), got %d", name, need, len(args))
	}
	if !star && len(args) > need {
		return nil, fmt.Errorf("alias %s takes %d argument(s), got %d", name, need, len(args))
	}
	for i, c := range cmds {
		var out []string
		for _, w := range c {
			if w == "$*" {
				out = append(out, args[need:]...)
				continue
			}
			out = append(out, placeholder.ReplaceAllStringFunc(w, func(m string) string {
				if m == "$*" {
					return strings.Join(args[need:], " ")
				}
				return args[m[1]-'1']
			}))
		}
		cmds[i] = out
	}
	return cmds, nil
}

// ownName is the name the connected node advertises and signs its channel messages with.
func ownName(l *meshgore.Link) string {
	return strings.Trim(l.Self.Name, "\x00 \t\r\n")
}

// runAlias runs the commands of an alias in order, stopping at the first that fails.
func runAlias(s *session, name, def string, args []string) int {
	node := func() (string, error) {
		l := s.companion()
		if l == nil {
			return "", errors.New("cannot connect to the node")
		}
		return ownName(l), nil
	}
	cmds, err := expandAlias(name, def, args, node)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mc:", err)
		return exitUsage
	}
	for _, words := range cmds {
		if meshgore.Debug {
			fmt.Printf("alias %s: %q\n", name, words)
		}
		c := lookup(words[0])
		if c == nil {
			fmt.Fprintf(os.Stderr, "mc: alias %s: unknown command %q\n", name, words[0])
			return exitUsage
		}
		if code := c.run(s, words[1:]); code != exitOK {
			return code
		}
	}
	return exitOK
}

// quoteWord quotes a word so that splitArgs reads it back as one word. A ";" word
// stays bare: in a definition it separates commands.
func quoteWord(w string) string {
	if w == ";" {
		return w
	}
	if w != "" && !strings.ContainsAny(w, " \t\n\"';") {
		return w
	}
	if !strings.Contains(w, `"`) {
		return `"` + w + `"`
	}
	return "'" + w + "'"
}

func cmdAlias(s *session, args []string) int {
	fs := newFlags("alias", "[-d NAME] | [NAME [=] COMMAND [; COMMAND]...]")
	del := fs.String("d", "", "remove this alias")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	aliases := loadAliases()
	switch {
	case *del != "":
		if _, found := aliases[*del]; !found {
			fmt.Printf("no alias %q\n", *del)
			return exitFail
		}
		delete(aliases, *del)
	case len(rest) == 0:
		if len(aliases) == 0 {
			fmt.Println(`No aliases. Define one with: alias test = send -wait 20s -only #test test`)
		}
		for _, name := range sortedKeys(aliases) {
			fmt.Printf("%s = %s\n", name, aliases[name])
		}
		return exitOK
	default:
		name := rest[0]
		rest = rest[1:]
		if len(rest) > 0 && rest[0] == "=" {
			rest = rest[1:]
		}
		if !aliasName.MatchString(name) {
			return usageError(fs, "%q is not a name: letters, digits, '_', '.', '-', starting with a letter", name)
		}
		if lookup(name) != nil {
			return usageError(fs, "%q is a command; an alias cannot replace it", name)
		}
		if len(rest) == 0 {
			return usageError(fs, "wants the commands %s stands for", name)
		}
		var def string
		if len(rest) == 1 { // alias x "send #a b": the one word is the whole definition
			def = rest[0]
		} else {
			q := make([]string, len(rest))
			for i, w := range rest {
				q[i] = quoteWord(w)
			}
			def = strings.Join(q, " ")
		}
		def = strings.TrimSpace(def)
		n := 0
		for _, text := range splitCommands(def) {
			words, err := splitArgs(text)
			if err != nil {
				return usageError(fs, "%v", err)
			}
			if len(words) == 0 {
				continue
			}
			if lookup(words[0]) == nil {
				return usageError(fs, "%q is not a command (an alias cannot use another alias)", words[0])
			}
			n++
		}
		if n == 0 {
			return usageError(fs, "wants the commands %s stands for", name)
		}
		aliases[name] = def
	}
	if err := saveAliases(aliases); err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	return exitOK
}

func init() {
	register(&command{"alias", "Aliases", "[-d NAME] | [NAME [=] COMMAND [; COMMAND]...]",
		"list, define or remove names for command lines ($1..$9 and $* stand for the words typed after the name, $node for this node's name)", cmdAlias})
}
