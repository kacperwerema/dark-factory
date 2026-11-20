package spend

import "strings"

// A read call is a shell command that only prints files: cat, head, tail,
// nl, sed -n, grep or rg on named files, optionally after cd <dir> and
// piped through filters (head, tail, cut, wc, sort, uniq). singleFileRead
// is such a call on exactly one file: what batching several reads into one
// call would save turns on.

// fileReaders are the commands that print or search named files.
var fileReaders = map[string]bool{"cat": true, "head": true, "tail": true, "nl": true, "sed": true, "bat": true, "grep": true, "rg": true}

// filters are what may follow a read in its pipeline.
var filters = map[string]bool{"head": true, "tail": true, "cut": true, "wc": true, "sort": true, "uniq": true, "nl": true}

// valueFlags are each reader's flags that take the next word.
var valueFlags = map[string]map[string]bool{
	"head": {"-n": true, "-c": true},
	"tail": {"-n": true, "-c": true},
	"grep": {"-e": true, "-f": true, "-A": true, "-B": true, "-C": true, "-m": true, "--include": true, "--exclude": true},
	"rg":   {"-e": true, "-f": true, "-A": true, "-B": true, "-C": true, "-m": true, "-g": true, "-t": true, "--glob": true, "--type": true},
	"sed":  {"-e": true, "-f": true},
}

func singleFileRead(command string) bool {
	files, ok := readCallFiles(command)
	return ok && files == 1
}

// readCallFiles is how many files a read call reads; ok is false for any
// other command.
func readCallFiles(command string) (int, bool) {
	cmds, ok := shellCommands(command)
	if !ok || len(cmds) == 0 {
		return 0, false
	}
	files := 0
	for i, pipeline := range cmds {
		if i == 0 && len(pipeline) == 1 && len(pipeline[0]) == 2 && pipeline[0][0] == "cd" {
			continue
		}
		n, ok := readStage(pipeline[0])
		if !ok {
			return 0, false
		}
		for _, st := range pipeline[1:] {
			if len(st) == 0 || !filters[st[0]] {
				return 0, false
			}
			for _, a := range st[1:] {
				if !strings.HasPrefix(a, "-") && !isNumber(a) {
					return 0, false // a filter reading a file of its own
				}
			}
		}
		files += n
	}
	return files, files > 0
}

// readStage is the files one reader reads; ok is false when it is no
// reader, writes (sed -i), or searches a folder or a glob.
func readStage(words []string) (int, bool) {
	if len(words) > 0 && strings.HasSuffix(words[0], "/grep") {
		words = append([]string{"grep"}, words[1:]...)
	}
	if len(words) > 1 && words[0] == "rtk" {
		words = words[1:]
		if words[0] == "read" {
			words[0] = "cat"
		}
	}
	if len(words) < 2 || !fileReaders[words[0]] {
		return 0, false
	}
	cmd := words[0]
	needPattern := cmd == "sed" || cmd == "grep" || cmd == "rg"
	sedQuiet := false
	files := 0
	for i := 1; i < len(words); i++ {
		a := words[i]
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			switch {
			case cmd == "sed" && (a == "-i" || strings.HasPrefix(a, "-i")):
				return 0, false
			case cmd == "sed" && a == "-n":
				sedQuiet = true
			case (cmd == "grep" || cmd == "rg") && (strings.ContainsAny(strings.TrimLeft(a, "-"), "rR") && !strings.HasPrefix(a, "--")):
				return 0, false // recursive: a search, not a read
			}
			if valueFlags[cmd][a] {
				if a == "-e" || a == "-f" {
					needPattern = false
				}
				i++
			}
			continue
		}
		if needPattern {
			needPattern = false
			continue
		}
		if strings.ContainsAny(a, "*?[") || strings.HasSuffix(a, "/") || a == "." {
			return 0, false
		}
		files++
	}
	if cmd == "sed" && !sedQuiet {
		return 0, false
	}
	return files, files > 0
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// shellCommands splits a command line into its commands (; && || and new
// lines), each a pipeline of stages, each stage its words with quotes
// taken off. ok is false for what a read call never holds: a variable or
// a command substitution, a heredoc, a redirection other than 2>&1 or
// 2>/dev/null, a background &, an open quote.
func shellCommands(line string) (cmds [][][]string, ok bool) {
	var (
		pipeline [][]string
		stage    []string
		word     strings.Builder
		inWord   bool
		quote    rune
	)
	endWord := func() {
		if inWord {
			stage = append(stage, word.String())
			word.Reset()
			inWord = false
		}
	}
	endStage := func() {
		endWord()
		if len(stage) > 0 {
			pipeline = append(pipeline, stage)
		}
		stage = nil
	}
	endCmd := func() {
		endStage()
		if len(pipeline) > 0 {
			cmds = append(cmds, pipeline)
		}
		pipeline = nil
	}
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			if quote == '"' && (r == '$' || r == '`') {
				return nil, false
			}
			word.WriteRune(r)
			continue
		}
		next := rune(0)
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		switch {
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == '$' || r == '`' || r == '<':
			return nil, false
		case r == '>':
			// Only 2>&1 and 2>/dev/null: the word so far is the 2.
			rest := string(rs[i:])
			if word.String() != "2" {
				return nil, false
			}
			switch {
			case strings.HasPrefix(rest, ">&1"):
				i += 2
			case strings.HasPrefix(rest, ">/dev/null"):
				i += len(">/dev/null") - 1
			default:
				return nil, false
			}
			word.Reset()
			inWord = false
		case r == '&' && next == '&', r == '|' && next == '|':
			endCmd()
			i++
		case r == '&':
			return nil, false
		case r == '|':
			endStage()
		case r == ';' || r == '\n':
			endCmd()
		case r == ' ' || r == '\t':
			endWord()
		case r == '\\' && next != 0:
			word.WriteRune(next)
			inWord = true
			i++
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	endCmd()
	return cmds, true
}
