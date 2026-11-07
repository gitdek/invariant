package tla

import "strings"

// ValidIncludeName accepts ASCII TLA+ identifiers in sibling .tla files,
// distinct from the main module. Callers still check regular files on read.
func ValidIncludeName(name, module string) bool {
	return strings.HasSuffix(name, ".tla") && isIdentifier(strings.TrimSuffix(name, ".tla")) && name != module+".tla"
}
