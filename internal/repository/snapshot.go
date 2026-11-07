package repository

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Snapshot holds a repository contract and the tree of its resolved base
// commit. Tree entries and configuration are private so callers cannot change
// the authority used by subsequent reads or ownership checks.
type Snapshot struct {
	commit   string
	checkout string
	config   Config
	entries  map[string]treeEntry
	blobsMu  sync.RWMutex
	blobs    map[string][]byte
	// linked holds what the base tree's trusted symbolic links name, in
	// lower case: a file, or a directory whose contents it serves (#705).
	linked map[string]bool
}

type treeEntry struct {
	mode string
	kind string
	oid  string
}

func (e treeEntry) regular() bool {
	return e.kind == "blob" && (e.mode == "100644" || e.mode == "100755")
}

func (e treeEntry) directory() bool {
	return e.kind == "tree" && e.mode == "040000"
}

// Load resolves baseRef once, then reads the contract and paths from that
// commit's tree. Declared commands are inert configuration, never executed.
// Only a missing contract in a successfully read base tree is ErrMissing.
func Load(ctx context.Context, checkout, baseRef string) (*Snapshot, error) {
	if strings.TrimSpace(baseRef) == "" || strings.HasPrefix(baseRef, "-") {
		return nil, fmt.Errorf("repository base must be an explicit commit or ref: %q", baseRef)
	}
	abs, err := filepath.Abs(checkout)
	if err != nil {
		return nil, fmt.Errorf("repository checkout: %w", err)
	}
	resolved, err := readGit(ctx, abs, "rev-parse", "--verify", "--end-of-options", baseRef+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolve repository base %q: %w", baseRef, err)
	}
	commit := strings.TrimSpace(string(resolved))
	if !objectID(commit) {
		return nil, fmt.Errorf("resolve repository base %q: invalid commit ID %q", baseRef, commit)
	}
	listing, err := readGit(ctx, abs, "ls-tree", "-r", "-t", "-z", "--full-tree", commit)
	if err != nil {
		return nil, fmt.Errorf("read repository tree %s: %w", commit, err)
	}
	entries, err := parseTree(listing)
	if err != nil {
		return nil, fmt.Errorf("read repository tree %s: %w", commit, err)
	}
	s := &Snapshot{commit: commit, checkout: abs, entries: entries}
	// Check ancestors before absence: a linked or non-directory .invariant
	// cannot turn an invalid contract location into an absent contract.
	if err := s.checkPath(Path, false, false); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, Path, err)
	}
	_, exists := entries[Path]
	if !exists {
		return nil, fmt.Errorf("%w: %s at %s", ErrMissing, Path, commit)
	}
	data, err := s.ReadFileContext(ctx, Path)
	if err != nil {
		return nil, fmt.Errorf("read %s at %s: %w", Path, commit, err)
	}
	s.config, err = decodeConfig(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, Path, err)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, Path, err)
	}
	if s.linked, err = s.linkTargets(ctx); err != nil {
		return nil, fmt.Errorf("read repository links at %s: %w", commit, err)
	}
	return s, nil
}

// linkTargets resolves, within the base tree, each symbolic link whose own
// path is trusted. An edit to what the link names changes what the trusted
// path serves, so that target is trusted too, and so is the target of a
// trusted link inside it. A link that leaves the repository or loops adds
// nothing: scope already refuses any change through it.
func (s *Snapshot) linkTargets(ctx context.Context) (map[string]bool, error) {
	var links []string
	for name, entry := range s.entries {
		if entry.mode == "120000" {
			links = append(links, name)
		}
	}
	sort.Strings(links)
	targets, done := map[string]bool{}, map[string]bool{}
	for grown := true; grown; {
		grown = false
		for _, link := range links {
			if done[link] || !s.trustedByRule(link) && !linkedFrom(targets, link) {
				continue
			}
			done[link], grown = true, true
			target, ok, err := s.resolve(ctx, link)
			if err != nil {
				return nil, err
			}
			if ok {
				targets[strings.ToLower(target)] = true
			}
		}
	}
	return targets, nil
}

// resolve follows a link, and any link on the way to its target, through the
// base tree. A target that doesn't exist resolves to its path all the same,
// since adding that file would change what the link serves.
func (s *Snapshot) resolve(ctx context.Context, name string) (string, bool, error) {
	current := name
	for range 40 {
		components := strings.Split(current, "/")
		at := -1
		for i := range components {
			if s.entries[strings.Join(components[:i+1], "/")].mode == "120000" {
				at = i
				break
			}
		}
		if at < 0 {
			return current, true, nil
		}
		link := strings.Join(components[:at+1], "/")
		target, err := readGit(ctx, s.checkout, "cat-file", "blob", s.entries[link].oid)
		if err != nil {
			return "", false, fmt.Errorf("read link %s: %w", link, err)
		}
		if path.IsAbs(string(target)) {
			return "", false, nil
		}
		current = path.Join(append([]string{path.Dir(link), string(target)}, components[at+1:]...)...)
		if current == ".." || strings.HasPrefix(current, "../") {
			return "", false, nil
		}
	}
	return "", false, nil
}

// linkedFrom says whether a trusted link names the path, or a directory that
// holds it. Names compare whatever their case, as agentPath's do.
func linkedFrom(targets map[string]bool, name string) bool {
	if targets["."] {
		return true
	}
	name = strings.ToLower(name)
	for dir := name; dir != "." && dir != "/"; dir = path.Dir(dir) {
		if targets[dir] {
			return true
		}
	}
	return false
}

// Commit returns the full commit ID captured by Load.
func (s *Snapshot) Commit() string { return s.commit }

// Config returns an independent copy of the captured configuration.
func (s *Snapshot) Config() Config { return cloneConfig(s.config) }

// ReadFile returns fresh bytes from a regular file in the captured tree. It
// reads the captured blob ID, never a ref, index entry or working-tree file.
func (s *Snapshot) ReadFile(name string) ([]byte, error) {
	return s.ReadFileContext(context.Background(), name)
}

// ReadFileContext reads the same captured blob as ReadFile, with the caller's
// cancellation and deadline applying to both cached bytes and object reads.
func (s *Snapshot) ReadFileContext(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.requireFile(name); err != nil {
		return nil, err
	}
	oid := s.entries[name].oid
	s.blobsMu.RLock()
	data, cached := s.blobs[oid]
	s.blobsMu.RUnlock()
	if !cached {
		// Keep reads outside the lock so each caller owns its cancellation.
		var err error
		data, err = readGit(ctx, s.checkout, "cat-file", "blob", oid)
		if err != nil {
			return nil, fmt.Errorf("read %s at %s: %w", name, s.commit, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.blobsMu.Lock()
		if s.blobs == nil {
			s.blobs = make(map[string][]byte)
		}
		s.blobs[oid] = data
		s.blobsMu.Unlock()
	}
	data = bytes.Clone(data)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

// validatePath checks repository-relative slash syntax. Only a part directory
// may be '.', and only a selector may end in a slash.
func validatePath(name string, allowRoot, selector bool) error {
	if name == "." && allowRoot {
		return nil
	}
	if name == "" || !utf8.ValidString(name) {
		return fmt.Errorf("invalid repository path %q", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("repository path %q contains a control character", name)
		}
	}
	if strings.ContainsAny(name, `\*?[]`) {
		return fmt.Errorf("repository path %q contains a backslash or glob", name)
	}
	clean := name
	if selector {
		clean = strings.TrimSuffix(clean, "/")
	}
	if clean == "" || clean == "." || path.IsAbs(clean) || path.Clean(clean) != clean {
		return fmt.Errorf("repository path %q must be clean and relative", name)
	}
	// A drive-qualified name is not repository-relative on every platform.
	if len(clean) >= 2 && clean[1] == ':' && ((clean[0] >= 'A' && clean[0] <= 'Z') || (clean[0] >= 'a' && clean[0] <= 'z')) {
		return fmt.Errorf("repository path %q is drive-qualified", name)
	}
	for _, component := range strings.Split(clean, "/") {
		if component == ".." || strings.EqualFold(component, ".git") {
			return fmt.Errorf("repository path %q contains traversal or Git metadata", name)
		}
	}
	return nil
}

// checkPath accepts absent files for new/deleted-file ownership, but rejects
// unsafe ancestors and leaves in the base tree. allowRoot is used for part
// directories; selector permits explicit directory prefixes ending in '/'.
func (s *Snapshot) checkPath(name string, allowRoot, selector bool) error {
	if err := validatePath(name, allowRoot, selector); err != nil {
		return err
	}
	if name == "." {
		return nil
	}
	clean := strings.TrimSuffix(name, "/")
	components := strings.Split(clean, "/")
	for i := range components {
		prefix := strings.Join(components[:i+1], "/")
		entry, exists := s.entries[prefix]
		if !exists {
			continue
		}
		if entry.mode == "120000" {
			return fmt.Errorf("repository path %q passes through symbolic link %q", name, prefix)
		}
		if entry.mode == "160000" || entry.kind == "commit" {
			return fmt.Errorf("repository path %q passes through gitlink %q", name, prefix)
		}
		if i < len(components)-1 || (selector && strings.HasSuffix(name, "/")) {
			if !entry.directory() {
				return fmt.Errorf("repository path %q requires directory %q", name, prefix)
			}
		} else if !allowRoot && !entry.regular() {
			return fmt.Errorf("repository path %q is not a regular file", name)
		}
	}
	return nil
}

func (s *Snapshot) requireFile(name string) error {
	if err := s.checkPath(name, false, false); err != nil {
		return err
	}
	entry, exists := s.entries[name]
	if !exists {
		return fmt.Errorf("%w: %q at %s", ErrFileMissing, name, s.commit)
	}
	if !entry.regular() {
		return fmt.Errorf("repository path %q is not a committed regular file", name)
	}
	return nil
}

func (s *Snapshot) requireDirectory(name string) error {
	if err := s.checkPath(name, true, false); err != nil {
		return err
	}
	if name == "." {
		return nil
	}
	entry, exists := s.entries[name]
	if !exists || !entry.directory() {
		return fmt.Errorf("repository path %q is not a committed directory", name)
	}
	return nil
}

func (s *Snapshot) checkSelector(name string) error {
	return s.checkPath(name, false, true)
}

func objectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func parseTree(data []byte) (map[string]treeEntry, error) {
	entries := make(map[string]treeEntry)
	for len(data) > 0 {
		end := bytes.IndexByte(data, 0)
		if end < 0 {
			return nil, fmt.Errorf("unterminated Git tree entry")
		}
		record := string(data[:end])
		data = data[end+1:]
		metadata, name, ok := strings.Cut(record, "\t")
		fields := strings.Fields(metadata)
		if !ok || name == "" || len(fields) != 3 || !objectID(fields[2]) {
			return nil, fmt.Errorf("malformed Git tree entry %q", record)
		}
		if _, exists := entries[name]; exists {
			return nil, fmt.Errorf("duplicate Git tree path %q", name)
		}
		entries[name] = treeEntry{mode: fields[0], kind: fields[1], oid: fields[2]}
	}
	return entries, nil
}

// readGit uses object plumbing only. Suppressing replacement refs is needed
// even with full object IDs: Git otherwise permits local replace refs to
// substitute different content. Environment overrides cannot redirect the
// selected checkout, and missing objects cannot trigger a remote lazy fetch.
func readGit(ctx context.Context, checkout string, args ...string) ([]byte, error) {
	options := []string{
		"--no-replace-objects", "-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false", "-c", "protocol.allow=never",
		"-c", "credential.helper=",
	}
	cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
	cmd.Dir = checkout
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GIT_") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ATTR_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
