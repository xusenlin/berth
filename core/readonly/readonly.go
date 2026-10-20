// Package readonly checks that a shell command only reads: it lets agents
// look around a server (logs, files, processes, sockets) without a way to
// change it.
//
// A command is parsed as POSIX sh and accepted only when every simple
// command in it, including those in $(…) and loops, is on a list of
// programs that cannot write files or run other programs, with the options
// that would (find -delete, sort -o, sed -i, journalctl --vacuum-size…)
// refused. Redirections may only read files or point at /dev/null and other
// descriptors; variable assignments, functions and background jobs are
// refused.
//
// The command runs with sh, which is bash on some distributions: Bash
// syntax that POSIX parses differently ($'…', brace expansion) is refused or
// treated as unknown, so that what is checked is what runs.
package readonly

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// MaxLen is the longest command accepted.
const MaxLen = 8 << 10

// Check parses a command and returns it in canonical form, which is what
// should run, or why it is refused.
func Check(cmd string) (string, error) {
	if strings.TrimSpace(cmd) == "" {
		return "", errors.New("empty command")
	}
	if len(cmd) > MaxLen {
		return "", fmt.Errorf("command longer than %d bytes", MaxLen)
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(cmd), "")
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	var bad error
	syntax.Walk(f, func(n syntax.Node) bool {
		if bad == nil {
			bad = node(n)
		}
		return bad == nil
	})
	if bad != nil {
		return "", bad
	}
	var b strings.Builder
	if err := syntax.NewPrinter().Print(&b, f); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Commands returns the names of the accepted programs, sorted.
func Commands() []string {
	names := make([]string, 0, len(free)+len(checked))
	for n := range free {
		names = append(names, n)
	}
	for n := range checked {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func node(n syntax.Node) error {
	switch n := n.(type) {
	case *syntax.Stmt:
		if n.Background || n.Coprocess {
			return errors.New("background jobs (&) are not allowed")
		}
		for _, r := range n.Redirs {
			if err := redirect(r); err != nil {
				return err
			}
		}
	case *syntax.CallExpr:
		if len(n.Assigns) > 0 {
			return errors.New("variable assignments are not allowed")
		}
		return call(n)
	case *syntax.FuncDecl:
		return errors.New("functions are not allowed")
	case *syntax.ProcSubst, *syntax.CoprocClause, *syntax.DeclClause, *syntax.LetClause, *syntax.TestClause,
		*syntax.ExtGlob, *syntax.ArithmCmd, *syntax.TimeClause:
		return errors.New("unsupported shell syntax: write it as POSIX sh")
	case *syntax.Lit:
		// $'…', $"…" and $[…] are Bash: POSIX reads a lone $.
		if strings.Contains(n.Value, "$") {
			return fmt.Errorf("unsupported %q: put a literal $ in single quotes", n.Value)
		}
	case *syntax.SglQuoted:
		if n.Dollar {
			return errors.New("$'…' is not allowed")
		}
	case *syntax.DblQuoted:
		if n.Dollar {
			return errors.New(`$"…" is not allowed`)
		}
	}
	return nil
}

func redirect(r *syntax.Redirect) error {
	switch r.Op {
	case syntax.RdrIn, syntax.Hdoc, syntax.DashHdoc:
		return nil
	case syntax.DplIn, syntax.DplOut:
		// Only descriptors: Bash reads >&file as &>file.
		if v, ok := static(r.Word); ok && (v == "-" || isDigits(v)) {
			return nil
		}
	case syntax.RdrOut, syntax.AppOut, syntax.ClbOut:
		if v, ok := static(r.Word); ok && v == "/dev/null" {
			return nil
		}
	}
	return errors.New("redirections may only read files, or write to /dev/null or another descriptor (2>&1)")
}

func call(c *syntax.CallExpr) error {
	args := c.Args
	name, ok := static(args[0])
	if !ok || hasExpansion(args[0]) {
		return errors.New("command names must be written out")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("%s: call programs by name, not by path", name)
	}
	if free[name] {
		return nil
	}
	sp, ok := checked[name]
	if !ok {
		return fmt.Errorf("%s is not allowed: only read-only commands may run (%s)", name, strings.Join(Commands(), " "))
	}
	if err := sp.check(name, args[1:]); err != nil {
		return err
	}
	if name == "sqlite3" {
		// Read-only, and safe mode: no .shell, .output, ATTACH, VACUUM
		// INTO, writefile() or extensions.
		c.Args = slices.Insert(c.Args, 1, lit("-readonly"), lit("-safe"))
	}
	return nil
}

func lit(s string) *syntax.Word { return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: s}}} }

// free are programs that only read, whatever their arguments.
var free = setOf(
	":", "[", "basename", "bzcat", "cat", "cd", "cksum", "cmp", "column", "comm", "cut", "df", "diff",
	"dirname", "du", "echo", "egrep", "expand", "false", "fgrep", "findmnt", "fold", "free", "getent",
	"grep", "groups", "head", "hexdump", "id", "iostat", "jq", "join", "last", "locale", "ls", "lsblk",
	"lscpu", "lsof", "md5sum", "mpstat", "netstat", "nl", "nproc", "od", "paste", "pgrep", "pidof",
	"printenv", "printf", "ps", "pwd", "readlink", "realpath", "rev", "seq", "sha1sum", "sha256sum",
	"sha512sum", "stat", "strings", "tac", "test", "top", "tr", "true", "uname", "unexpand", "uptime",
	"vmstat", "w", "wc", "which", "who", "whoami", "xzcat", "zcat", "zgrep",
)

// spec says which arguments of a program would write or run something.
type spec struct {
	denyShort string   // short options refused
	argShort  string   // short options that take a value
	denyLong  []string // long options refused, also when abbreviated
	argLong   []string // long options whose value may be the next word
	denyWords []string // words refused anywhere (find primaries, ip verbs)
	subs      []string // allowed first operands; none means any
	// maxOperands caps the operands, beyond which some programs write
	// (uniq IN OUT); -1 is no cap.
	maxOperands int
	// operands checks the operands, e.g. sed's script.
	operands func(ops []string) error
	// custom replaces the generic check.
	custom func(args []*syntax.Word) error
}

var checked = map[string]spec{
	"date": {denyShort: "s", argShort: "dfr", denyLong: []string{"set"}, maxOperands: -1,
		operands: func(ops []string) error {
			for _, o := range ops {
				if !strings.HasPrefix(o, "+") {
					return errors.New("date: only +FORMAT operands, setting the clock is not allowed")
				}
			}
			return nil
		}},
	"dmesg": {denyShort: "cCnDEwW", argShort: "fFls", maxOperands: -1,
		denyLong: []string{"clear", "read-clear", "console-level", "console-off", "console-on", "follow", "follow-new"}},
	"file": {denyShort: "C", argShort: "efFmP", denyLong: []string{"compile"}, maxOperands: -1},
	"find": {maxOperands: -1, denyWords: []string{"-delete", "-exec", "-execdir", "-ok", "-okdir",
		"-fprint", "-fprint0", "-fprintf", "-fls"}},
	"hostname":    {denyShort: "bF", denyLong: []string{"boot", "file"}, maxOperands: 0},
	"hostnamectl": {subs: []string{"status"}, maxOperands: 1},
	"ip":          {custom: checkIP},
	"journalctl": {denyShort: "f", argShort: "bcDFgIMnopStTuU", maxOperands: -1,
		argLong: []string{"unit", "user-unit", "identifier", "lines", "priority", "output", "since", "until", "boot",
			"directory", "file", "grep", "field", "cursor", "after-cursor", "facility", "namespace", "machine"},
		denyLong: []string{"follow", "vacuum-size", "vacuum-time", "vacuum-files", "rotate", "flush", "sync",
			"relinquish-var", "smart-relinquish-var", "setup-keys", "update-catalog", "new-id128"}},
	"sed":     {custom: checkSed},
	"sqlite3": {custom: checkSQLite},
	"sort":    {denyShort: "o", argShort: "kStT", denyLong: []string{"output", "compress-program"}, maxOperands: -1},
	"ss":      {denyShort: "KN", argShort: "fAF", denyLong: []string{"kill", "net"}, maxOperands: -1},
	"systemctl": {denyShort: "HM", argShort: "tpnos", denyLong: []string{"host", "machine"}, maxOperands: -1,
		argLong: []string{"type", "property", "state", "lines", "output", "signal", "what", "root"},
		subs: []string{"status", "show", "cat", "list-units", "list-unit-files", "list-timers", "list-sockets",
			"list-dependencies", "list-jobs", "list-machines", "is-active", "is-enabled", "is-failed",
			"is-system-running", "get-default", "show-environment"}},
	"tail":        {denyShort: "fF", argShort: "cns", denyLong: []string{"follow"}, maxOperands: -1},
	"timedatectl": {subs: []string{"status", "show", "list-timezones", "timesync-status", "show-timesync"}, maxOperands: -1},
	"tree":        {denyShort: "o", argShort: "LPIH", maxOperands: -1},
	"uniq":        {argShort: "fsw", maxOperands: 1},
	"xargs":       {custom: checkXargs},
	"xxd":         {denyShort: "r", argShort: "cglosC", denyLong: []string{"revert"}, maxOperands: 1},
}

// word is what can be told of an argument before the shell expands it.
type word struct {
	value  string // after quote removal, when static
	static bool   // no expansion at all
	// split is true when the shell may make several words of it: unquoted
	// $x or $(…), or a glob or brace pattern.
	split bool
	// lead is the first character for sure, or 0 when an expansion comes
	// first.
	lead byte
}

func classify(w *syntax.Word) word {
	var out word
	out.static = true
	var b strings.Builder
	first := true
	lead := func(s string, known bool) {
		if first {
			first = false
			if known && s != "" {
				out.lead = s[0]
			}
		}
	}
	for _, p := range w.Parts {
		switch p := p.(type) {
		case *syntax.Lit:
			v := unescape(p.Value, false)
			if strings.ContainsAny(p.Value, "*?[{") {
				// A pattern: unknown words, but they start with what
				// precedes the first special character.
				out.split = true
				i := strings.IndexAny(p.Value, "*?[{")
				lead(unescape(p.Value[:i], false), true)
			}
			lead(v, true)
			b.WriteString(v)
		case *syntax.SglQuoted:
			lead(p.Value, true)
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				if l, ok := q.(*syntax.Lit); ok {
					v := unescape(l.Value, true)
					lead(v, true)
					b.WriteString(v)
				} else {
					out.static = false
					lead("", false)
				}
			}
			if len(p.Parts) == 0 {
				continue
			}
		default:
			out.static, out.split = false, true
			lead("", false)
		}
	}
	if !out.static {
		return word{split: out.split, lead: out.lead}
	}
	out.value = b.String()
	if out.split {
		out.static = false
	}
	return out
}

// static returns the value of a word with no expansion and no pattern.
func static(w *syntax.Word) (string, bool) {
	c := classify(w)
	return c.value, c.static
}

func hasExpansion(w *syntax.Word) bool {
	for _, p := range w.Parts {
		switch p.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
		case *syntax.DblQuoted:
			for _, q := range p.(*syntax.DblQuoted).Parts {
				if _, ok := q.(*syntax.Lit); !ok {
					return true
				}
			}
		default:
			return true
		}
	}
	return false
}

// unescape removes the backslashes the shell removes, unquoted or within
// double quotes.
func unescape(s string, dq bool) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			next := s[i+1]
			if !dq || strings.IndexByte("$`\"\\\n", next) >= 0 {
				i++
				if next != '\n' {
					b.WriteByte(next)
				}
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (sp spec) check(name string, args []*syntax.Word) error {
	if sp.custom != nil {
		return sp.custom(args)
	}
	var ops []string
	dashdash := false
	for i := 0; i < len(args); i++ {
		w := classify(args[i])
		if !w.static {
			// After --, nothing is an option, but find reads its expression
			// after it too.
			if dashdash && sp.denyWords == nil && !w.split {
				if sp.maxOperands >= 0 || sp.operands != nil || (len(sp.subs) > 0 && len(ops) == 0) {
					return fmt.Errorf("%s: write argument %d out, without $ or patterns", name, i+1)
				}
				ops = append(ops, "")
				continue
			}
			if w.lead == 0 || w.lead == '-' || strings.IndexByte("*?[{", w.lead) >= 0 {
				return fmt.Errorf("%s: write argument %d out, start it with a path such as ./ or /, or quote it after --", name, i+1)
			}
			if sp.maxOperands >= 0 || sp.operands != nil || (len(sp.subs) > 0 && len(ops) == 0) {
				return fmt.Errorf("%s: write argument %d out, without $ or patterns", name, i+1)
			}
			ops = append(ops, "")
			continue
		}
		v := w.value
		if slices.Contains(sp.denyWords, v) {
			return fmt.Errorf("%s %s is not allowed: it changes the server", name, v)
		}
		switch {
		case dashdash || v == "-" || !strings.HasPrefix(v, "-"):
			ops = append(ops, v)
		case v == "--":
			dashdash = true
		case strings.HasPrefix(v, "--"):
			opt, _, hasValue := strings.Cut(v[2:], "=")
			for _, d := range sp.denyLong {
				if strings.HasPrefix(d, opt) {
					return fmt.Errorf("%s --%s is not allowed: it changes the server or does not end", name, d)
				}
			}
			if !hasValue && slices.Contains(sp.argLong, opt) {
				if err := value(name, args, &i); err != nil {
					return err
				}
			}
		default:
			for j := 1; j < len(v); j++ {
				if strings.IndexByte(sp.denyShort, v[j]) >= 0 {
					return fmt.Errorf("%s -%c is not allowed: it changes the server or does not end", name, v[j])
				}
				if strings.IndexByte(sp.argShort, v[j]) >= 0 {
					if j == len(v)-1 {
						if err := value(name, args, &i); err != nil {
							return err
						}
					}
					break
				}
			}
		}
	}
	if len(sp.subs) > 0 && len(ops) > 0 && !slices.Contains(sp.subs, ops[0]) {
		return fmt.Errorf("%s %s is not allowed: only %s", name, ops[0], strings.Join(sp.subs, ", "))
	}
	if sp.maxOperands >= 0 && len(ops) > sp.maxOperands {
		return fmt.Errorf("%s: at most %d operands, more would change the server", name, sp.maxOperands)
	}
	if sp.operands != nil {
		return sp.operands(ops)
	}
	return nil
}

// value skips the value of an option, which must stay one word.
func value(name string, args []*syntax.Word, i *int) error {
	*i++
	if *i < len(args) && classify(args[*i]).split {
		return fmt.Errorf("%s: write argument %d out, without patterns or unquoted $", name, *i+1)
	}
	return nil
}

// checkXargs accepts xargs running a program that only reads, whatever
// arguments it is given.
func checkXargs(args []*syntax.Word) error {
	const argShort = "ILnPsdEa"
	for i := 0; i < len(args); i++ {
		v, ok := static(args[i])
		if !ok {
			return errors.New("xargs: write its options and command out")
		}
		switch {
		case v == "--":
			return xargsCommand(args[i+1:])
		case strings.HasPrefix(v, "--"):
			opt, _, _ := strings.Cut(v[2:], "=")
			for _, d := range []string{"interactive", "open-tty", "process-slot-var"} {
				if strings.HasPrefix(d, opt) {
					return fmt.Errorf("xargs --%s is not allowed", d)
				}
			}
		case strings.HasPrefix(v, "-") && len(v) > 1:
			for j := 1; j < len(v); j++ {
				if v[j] == 'p' || v[j] == 'o' {
					return fmt.Errorf("xargs -%c is not allowed", v[j])
				}
				if strings.IndexByte(argShort, v[j]) >= 0 {
					if j == len(v)-1 {
						i++
					}
					break
				}
			}
		default:
			return xargsCommand(args[i:])
		}
	}
	return nil // xargs alone runs echo
}

func xargsCommand(args []*syntax.Word) error {
	if len(args) == 0 {
		return nil
	}
	name, ok := static(args[0])
	if !ok || hasExpansion(args[0]) || !free[name] || name == "cd" {
		return fmt.Errorf("xargs may only run programs that read whatever their arguments: %s", strings.Join(xargsAllowed(), " "))
	}
	return nil
}

// checkIP accepts ip showing objects. ip takes any prefix of an option or
// verb (ip link s is set), so verbs must be spelt out.
func checkIP(args []*syntax.Word) error {
	var ops []string
	for i := 0; i < len(args); i++ {
		v, ok := static(args[i])
		if !ok {
			return errors.New("ip: write its arguments out")
		}
		if strings.HasPrefix(v, "-") {
			opt := strings.TrimLeft(v, "-")
			if opt == "" || opt == "b" || opt == "n" {
				return fmt.Errorf("ip %s is not allowed", v)
			}
			for _, d := range []string{"batch", "force", "netns"} {
				if strings.HasPrefix(d, opt) {
					return fmt.Errorf("ip -%s is not allowed", d)
				}
			}
			if opt == "f" || strings.HasPrefix("family", opt) && len(opt) > 1 {
				i++
			}
			continue
		}
		ops = append(ops, v)
	}
	if len(ops) == 0 {
		return nil
	}
	if strings.HasPrefix("netns", ops[0]) && len(ops[0]) > 1 || strings.HasPrefix("monitor", ops[0]) && len(ops[0]) > 1 {
		return fmt.Errorf("ip %s is not allowed", ops[0])
	}
	if len(ops) > 1 && !slices.Contains([]string{"sh", "sho", "show", "list", "lst", "ls", "get"}, ops[1]) {
		return fmt.Errorf("ip %s %s is not allowed: only show, list or get, spelt out", ops[0], ops[1])
	}
	return nil
}

// checkSed accepts sed printing: no -i, no script file, and scripts of
// commands that neither write files nor run programs.
func checkSed(args []*syntax.Word) error {
	script := false
	var files []*syntax.Word
	for i := 0; i < len(args); i++ {
		w := classify(args[i])
		if !w.static {
			files = append(files, args[i])
			continue
		}
		v := w.value
		next := func() (string, error) {
			i++
			if i >= len(args) {
				return "", errors.New("sed: missing script")
			}
			s, ok := static(args[i])
			if !ok {
				return "", errors.New("sed: write the script out, in single quotes")
			}
			return s, nil
		}
		switch {
		case v == "-" || !strings.HasPrefix(v, "-"):
			if !script {
				if err := sedScript(v); err != nil {
					return err
				}
				script = true
				continue
			}
			files = append(files, args[i])
		case strings.HasPrefix(v, "--"):
			opt, val, hasValue := strings.Cut(v[2:], "=")
			switch {
			case slices.Contains([]string{"quiet", "silent", "regexp-extended", "unbuffered", "null-data",
				"separate", "posix", "debug", "sandbox", "line-length"}, opt):
			case opt == "expression":
				if !hasValue {
					s, err := next()
					if err != nil {
						return err
					}
					val = s
				}
				if err := sedScript(val); err != nil {
					return err
				}
				script = true
			default:
				return fmt.Errorf("sed --%s is not allowed: write out --quiet, --regexp-extended, --expression…", opt)
			}
		default:
			for j := 1; j < len(v); j++ {
				switch v[j] {
				case 'n', 'E', 'r', 'u', 'z', 's':
					continue
				case 'e', 'l':
					val := v[j+1:]
					if val == "" {
						s, err := next()
						if err != nil {
							return err
						}
						val = s
					}
					if v[j] == 'e' {
						if err := sedScript(val); err != nil {
							return err
						}
						script = true
					}
				default:
					return fmt.Errorf("sed -%c is not allowed", v[j])
				}
				break
			}
		}
	}
	for _, f := range files {
		w := classify(f)
		if w.lead == 0 || w.lead == '-' || strings.IndexByte("*?[{", w.lead) >= 0 {
			return errors.New("sed: write file names out, or start them with a path such as ./ or /")
		}
	}
	return nil
}

// checkSQLite accepts sqlite3 with output options; call adds -readonly
// and -safe. sqlite3 reads options after the database too, so no argument
// may turn into one.
func checkSQLite(args []*syntax.Word) error {
	plain := []string{"readonly", "safe", "json", "csv", "html", "line", "list", "column", "box", "table",
		"markdown", "tabs", "quote", "ascii", "header", "noheader", "bail", "batch", "echo", "version"}
	valued := []string{"separator", "newline", "nullvalue", "escape"}
	for i := 0; i < len(args); i++ {
		w := classify(args[i])
		if !w.static {
			if w.lead == 0 || w.lead == '-' || strings.IndexByte("*?[{", w.lead) >= 0 {
				return fmt.Errorf("sqlite3: write argument %d out, or start it with a path such as ./ or /", i+1)
			}
			continue
		}
		if !strings.HasPrefix(w.value, "-") {
			continue
		}
		opt := strings.TrimLeft(w.value, "-")
		switch {
		case slices.Contains(plain, opt):
		case slices.Contains(valued, opt):
			if err := value("sqlite3", args, &i); err != nil {
				return err
			}
		default:
			return fmt.Errorf("sqlite3 %s is not allowed: only output options such as -json, -header, -column", w.value)
		}
	}
	return nil
}

// sedScript accepts sed commands that print, delete or substitute, without
// the e and w flags.
func sedScript(s string) error {
	i, n := 0, len(s)
	bad := func(why string) error { return fmt.Errorf("sed script %q: %s", s, why) }
	digits := func() {
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	spaces := func() {
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
	}
	// delimited skips past the next unescaped delim.
	delimited := func(delim byte) error {
		for i < n {
			switch s[i] {
			case '\\':
				i += 2
				continue
			case delim:
				i++
				return nil
			case '\n':
				return bad("unterminated")
			}
			i++
		}
		return bad("unterminated")
	}
	addr := func() error {
		switch {
		case i < n && s[i] >= '0' && s[i] <= '9':
			digits()
			if i < n && s[i] == '~' {
				i++
				digits()
			}
		case i < n && s[i] == '$':
			i++
		case i < n && (s[i] == '/' || s[i] == '\\'):
			delim := byte('/')
			if s[i] == '\\' {
				i++
				if i >= n {
					return bad("unterminated")
				}
				delim = s[i]
			}
			i++
			if err := delimited(delim); err != nil {
				return err
			}
			for i < n && (s[i] == 'I' || s[i] == 'M') {
				i++
			}
		}
		return nil
	}
	for {
		for i < n && strings.IndexByte(" \t\n;}", s[i]) >= 0 {
			i++
		}
		if i >= n {
			return nil
		}
		if err := addr(); err != nil {
			return err
		}
		if i < n && s[i] == ',' {
			i++
			if i < n && (s[i] == '+' || s[i] == '~') {
				i++
				digits()
			} else if err := addr(); err != nil {
				return err
			}
		}
		spaces()
		if i < n && s[i] == '!' {
			i++
			spaces()
		}
		if i >= n {
			return bad("missing command")
		}
		c := s[i]
		i++
		switch c {
		case '{':
			continue
		case 'p', 'P', 'd', 'D', 'g', 'G', 'h', 'H', 'x', 'n', 'N', '=', 'z':
		case 'q', 'Q', 'l':
			spaces()
			digits()
		case 's', 'y':
			if i >= n || s[i] == '\n' || s[i] == '\\' {
				return bad("bad delimiter")
			}
			delim := s[i]
			i++
			if err := delimited(delim); err != nil {
				return err
			}
			if err := delimited(delim); err != nil {
				return err
			}
			for c == 's' && i < n && strings.IndexByte("gGiIpmM0123456789", s[i]) >= 0 {
				i++
			}
		default:
			return bad(fmt.Sprintf("command %c is not allowed: only p, d, s, y, q, =, l and hold-space commands", c))
		}
		spaces()
		if i < n && strings.IndexByte(";\n}", s[i]) < 0 {
			return bad(fmt.Sprintf("unexpected %q, the e and w flags are not allowed", s[i]))
		}
	}
}

func xargsAllowed() []string {
	var names []string
	for n := range free {
		if n != "cd" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
