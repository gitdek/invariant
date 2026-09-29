package factory

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

// pathPart is one part of a path in free-form text. It runs up to a slash, a
// space, a quote, a bracket, or the punctuation after a path, such as the
// colon in Go's errors.
const pathPart = `[^/\s"'\x60()\[\]{}<>,;:]+`

// absPath is an absolute path in free-form text, such as an error's. It
// starts a word, after a space, a quote or backtick, an opening bracket or
// an equals sign, and has at least two parts. A URL's path is inside a word,
// and a command such as /invariant retry has one part, so neither is one.
var absPath = regexp.MustCompile(`(?:^|[\s"'\x60(\[{<=])/` + pathPart + `(?:/+` + pathPart + `)+`)

// localPaths rewrites the paths on this machine in text from a run, which
// the factory saves under refs/invariant/runs/ and posts, where anyone can
// read it (#113). A path under work, the watcher's work directory, is
// written relative to it, such as issue-91/build-20260929-101010/result, so
// it still says where. Any other absolute path is written as its last part:
// /Users/someone/.local/bin/claude reads claude. Either way, the text no
// longer names the watcher's own path, and its user, as #91's failure
// comment did.
func localPaths(text, work string) string {
	if abs, err := filepath.Abs(work); err == nil {
		work = abs
	}
	return absPath.ReplaceAllStringFunc(text, func(m string) string {
		// The match starts with the character before the path, if there's one.
		i := strings.Index(m, "/")
		before, p := m[:i], m[i:]
		if rel, err := filepath.Rel(work, p); err == nil && filepath.IsLocal(rel) {
			return before + rel
		}
		return before + filepath.Base(p)
	})
}

// localError is err with its text rewritten by localPaths, or nil.
func localError(err error, work string) error {
	if err == nil {
		return nil
	}
	return errors.New(localPaths(err.Error(), work))
}
