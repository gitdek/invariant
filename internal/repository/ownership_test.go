package repository

import "testing"

func TestRepositoryContractGlobalSelectionStillChecksEveryPath(t *testing.T) {
	root := repo461(t, mixed461())
	snapshot := load461(t, root, commit461(t, root))
	for _, global := range []string{Path, "AGENTS.md", "SPEC.md", "README.md"} {
		for _, bad := range []string{"unowned/file.go", "../outside", "api"} {
			for _, paths := range [][]string{{global, bad}, {bad, global}} {
				if parts, err := snapshot.PartsFor(paths); err == nil || len(parts) != 0 {
					t.Errorf("PartsFor(%q) = %v, %v; want no parts and an error", paths, parts, err)
				}
			}
		}
	}
}
