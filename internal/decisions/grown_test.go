package decisions

import "testing"

// Base's journal file must be the start of the checkout's, line by line,
// through factory/journal-merges (#194): lines may follow base's, and none
// of base's may change, move or go, even when its bytes would still start
// the checkout's file.
func TestAJournalFileOnlyGrowsByWholeLines(t *testing.T) {
	for _, c := range []struct {
		name, was, now string
		starts         bool
	}{
		{"unchanged", "a\nb\n", "a\nb\n", true},
		{"lines added", "a\nb\n", "a\nb\nc\nd\n", true},
		{"new file", "", "a\n", true},
		{"emptied", "a\n", "", false},
		{"a line changed", "a\nb\n", "a\nB\n", false},
		{"a line gone", "a\nb\nc\n", "a\nc\n", false},
		{"lines swapped", "a\nb\n", "b\na\n", false},
		{"a line put before", "a\n", "z\na\n", false},
		{"the same line twice", "a\na\n", "a\na\na\n", true},
		{"a last line without its newline, extended", "a\nb", "a\nbc\n", false},
		{"a last line that lost its newline", "a\nb\n", "a\nb", false},
	} {
		got, err := startsJournal([]byte(c.was), []byte(c.now))
		if err != nil || got != c.starts {
			t.Errorf("%s: %q starts %q = %v, %v; want %v", c.name, c.was, c.now, got, err, c.starts)
		}
	}
}
