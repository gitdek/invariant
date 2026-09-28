package decisions

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// jsonLine is an event as its journal line holds it.
func jsonLine(e Event) ([]byte, error) { return json.Marshal(e) }

// citeFrom adds an edge from src to every other decision its words mention,
// as a citation, unless src already has an edge of its own to that one.
func citeFrom(tx *sql.Tx, src, project, text string) error {
	for _, m := range mentionedID.FindAllString(text, -1) {
		dst := project + "/" + m
		if dst == src {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO edge (src, type, dst, project, derived)
			SELECT ?1, 'cites', ?2, ?3, 1 WHERE NOT EXISTS (SELECT 1 FROM edge WHERE src = ?1 AND dst = ?2)`, src, dst, project); err != nil {
			return err
		}
	}
	return nil
}

// Files the scan reads, by extension, and the kind of node a line of each
// becomes.
var (
	codeFiles = map[string]bool{".go": true, ".ts": true, ".js": true, ".cjs": true, ".py": true, ".tla": true, ".yml": true, ".yaml": true, ".sh": true}
	docFiles  = map[string]bool{".md": true}
	// skipped: version control, dependencies, build output, test copies, and
	// the journal itself.
	skipDirs = map[string]bool{".git": true, "node_modules": true, "out": true, "bin": true, "vendor": true, "testdata": true}
	// a decision's full record, whose citations are the decision's own
	recordFile = regexp.MustCompile(`^decisions/(D-\d{4,})-[^/]*\.md$`)
)

// deriveCitations reads every decision's words, and every line of the
// repository's docs and code that mentions a decision, into edges. A line of
// code implements the decisions it names, and a line of the SPEC or the docs
// cites them. decisions/log.md is left out: it's a view of the store.
func deriveCitations(tx *sql.Tx, repo Repo) error {
	rows, err := tx.Query(`SELECT id, text FROM node WHERE kind = 'decision' AND project = ?`, repo.Name)
	if err != nil {
		return err
	}
	type said struct{ id, text string }
	var all []said
	for rows.Next() {
		var s said
		if err := rows.Scan(&s.id, &s.text); err != nil {
			rows.Close()
			return err
		}
		all = append(all, s)
	}
	rows.Close()
	for _, s := range all {
		if err := citeFrom(tx, s.id, repo.Name, s.text); err != nil {
			return err
		}
	}
	return filepath.WalkDir(repo.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repo.Dir, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (skipDirs[d.Name()] || rel == "decisions/journal") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if (!codeFiles[ext] && !docFiles[ext]) || rel == "decisions/log.md" || !d.Type().IsRegular() {
			return nil
		}
		return scanFile(tx, repo, path, rel, ext)
	})
}

// scanFile reads one file's mentions of decisions.
func scanFile(tx *sql.Tx, repo Repo, path, rel, ext string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	kind, edge := "doc", Cites
	switch {
	case rel == "SPEC.md":
		kind = "spec"
	case codeFiles[ext]:
		kind, edge = "code", Implements
	}
	record := ""
	if m := recordFile.FindStringSubmatch(rel); m != nil {
		record = repo.Name + "/" + m[1]
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		ids := mentionedID.FindAllString(line, -1)
		if len(ids) == 0 {
			continue
		}
		if record != "" {
			if err := citeFrom(tx, record, repo.Name, line); err != nil {
				return err
			}
			continue
		}
		src := repo.Name + "/" + rel + ":" + strconv.Itoa(n)
		text := strings.TrimSpace(line)
		if len(text) > 400 {
			text = text[:400] + "…"
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO node (id, kind, project, text) VALUES (?, ?, ?, ?)`, src, kind, repo.Name, text); err != nil {
			return err
		}
		for _, m := range ids {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO edge (src, type, dst, project, derived) VALUES (?, ?, ?, ?, 1)`, src, edge, repo.Name+"/"+m, repo.Name); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}
