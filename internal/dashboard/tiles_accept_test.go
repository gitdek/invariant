package dashboard

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// acceptTile is the tile keyed k: bad, states, stmts, bugs, proved or dec.
func acceptTile(t *testing.T, tiles []Tile, k string) Tile {
	t.Helper()
	for _, x := range tiles {
		if x.Key == k {
			return x
		}
	}
	t.Fatalf("no tile is keyed %s among %d tiles", k, len(tiles))
	return Tile{}
}

// acceptInFull is n as the page writes a number in full: 1,285,100.
func acceptInFull(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// acceptAll is totals with n of everything a tile counts: n merges without a
// green gate, n states, n statements, n of n planted bugs caught, n functions
// proved and n decisions.
func acceptAll(n int64) Totals {
	c := int(n)
	return Totals{Projects: 2, Models: 2, States: n, Statements: c, Bugs: c, BugsCaught: c, Proved: c,
		Merged: c, MergesChecked: c, BadMerges: c, LocksChecked: 1, Decisions: c}
}

// acceptEms is the most room a number a tile shows can take, in em, in the
// page's font: Space Grotesk at weight 600 with tabular figures, or a system
// font in its place, with no credit for the page's tighter letter spacing. A
// figure takes at most 0.7 em, a comma or a point 0.4, a slash 0.6, and any
// other sign, such as M or B, 1.
func acceptEms(s string) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			w += 0.7
		case r == ',' || r == '.':
			w += 0.4
		case r == '/':
			w += 0.6
		default:
			w += 1
		}
	}
	return w
}

// acceptLayout is the numbers row as style.css lays it out on a page w px
// wide. The page's main column is at most 1,280 px, inside a gutter of
// clamp(16px, 4vw, 40px) on each side. The row has a 1 px border and 1 px
// between its tiles: six to a row, three at 1,100 px or less, and two at 560
// px or less. Each tile is padded 20 px on each side, or 14 px at 560 px or
// less, and a number's size is clamp(30px, 3.4vw, 42px) before it's fitted.
// It returns the tiles to a row, the width inside each, and that size.
func acceptLayout(w float64) (perRow int, inside, size float64) {
	gutter := math.Min(40, math.Max(16, 0.04*w))
	row := math.Min(w, 1280) - 2*gutter - 2
	perRow, pad := 6, 20.0
	switch {
	case w <= 560:
		perRow, pad = 2, 14
	case w <= 1100:
		perRow = 3
	}
	inside = (row-float64(perRow-1))/float64(perRow) - 2*pad
	return perRow, inside, math.Min(42, math.Max(30, 0.034*w))
}

// A tile's number of a million or more shows short, and its small text and
// tooltip give it exactly. Below a million, a number shows in full, as it
// does now.
func TestABigNumberShowsShortWithItsExactNumberNearby(t *testing.T) {
	states := func(n int64) Tile {
		return acceptTile(t, Tiles(Totals{Projects: 16, Models: 15, States: n}, Who{}), "states")
	}
	const exact = "1,285,100 states, across 15 models, exhaustively, within the ratified bounds"
	if x := states(1285100); x.Shown != "1.29M" || x.Small != exact || x.Title != exact {
		t.Errorf("1,285,100 states show as %q, with small text %q and tooltip %q; want 1.29M, with %q as both", x.Shown, x.Small, x.Title, exact)
	}
	if x := states(999999); x.Shown != "999,999" || x.Small != "across 15 models, exhaustively, within the ratified bounds" {
		t.Errorf("999,999 states show as %q, with small text %q; want 999,999 in full, with the small text as it is now", x.Shown, x.Small)
	}
	for n, want := range map[int64]string{
		0: "0", 7: "7", 1000: "1,000", 999999: "999,999",
		1000000: "1M", 1500000: "1.5M", 12345678: "12.3M", 123456789: "123M",
		999999999: "1B", 1234567890: "1.23B",
	} {
		x := states(n)
		if x.Shown != want {
			t.Errorf("%s states show as %q; want %q", acceptInFull(n), x.Shown, want)
		}
		if n >= 1000000 && (!strings.HasPrefix(x.Small, acceptInFull(n)+" states, ") || !strings.Contains(x.Title, acceptInFull(n))) {
			t.Errorf("%s states show as %q, with small text %q and tooltip %q; both must give the number exactly", acceptInFull(n), x.Shown, x.Small, x.Title)
		}
	}

	var keys []string
	for _, x := range Tiles(acceptAll(3456789), Who{Ratified: 1, Logged: 2}) {
		keys = append(keys, x.Key)
		if !strings.Contains(x.Shown, "3.46M") || strings.Contains(x.Shown, "3,456,789") || !strings.Contains(x.Small, "3,456,789") || !strings.Contains(x.Title, "3,456,789") {
			t.Errorf("the %s tile of 3,456,789 shows %q, with small text %q and tooltip %q; want it short, as 3.46M, and exactly in both", x.Key, x.Shown, x.Small, x.Title)
		}
	}
	if got := strings.Join(keys, " "); got != "bad states stmts bugs proved dec" {
		t.Errorf("the tiles are %s; want bad states stmts bugs proved dec, as the page shows them now", got)
	}
	for _, x := range Tiles(acceptAll(999999), Who{Ratified: 1, Logged: 2}) {
		want := "999,999"
		if x.Key == "bugs" {
			want = "999,999/999,999"
		}
		if x.Shown != want {
			t.Errorf("the %s tile of 999,999 shows %q; want %q, in full", x.Key, x.Shown, want)
		}
	}
}

// Every tile's number comes with its width in em, at least what its signs can
// take in the page's font. Set no larger than its tile's inside width over
// that, as the page sets it, it fits inside its tile at every width the page
// lays out for: six tiles to a row, three or two. And the tiles of totals
// like today's, 1.29M among them, are never set smaller than 30 px, the
// smallest the page sets a number now.
func TestEveryTilesNumberFitsItsTileAtEveryWidth(t *testing.T) {
	today := Totals{Projects: 18, Models: 15, States: 1285100, Statements: 164, Bugs: 30, BugsCaught: 30, Proved: 96,
		Merged: 41, MergesChecked: 41, LocksChecked: 12, Decisions: 131}
	for _, c := range []struct {
		name   string
		totals Totals
		today  bool
	}{
		{"totals like today's", today, true},
		{"no totals yet", Totals{}, true},
		{"999,999 of everything", acceptAll(999999), false},
		{"a million of everything", acceptAll(1000000), false},
		{"999,999,999,999 of everything", acceptAll(999999999999), false},
	} {
		tiles := Tiles(c.totals, Who{Ratified: 60, Logged: 55})
		if len(tiles) != 6 {
			t.Errorf("%s: %d tiles; want 6", c.name, len(tiles))
		}
		for _, x := range tiles {
			need := acceptEms(x.Shown)
			if x.Ems < need-1e-9 {
				t.Errorf("%s: the %s tile says its %q is %.2f em wide; it can take %.2f em", c.name, x.Key, x.Shown, x.Ems, need)
				continue
			}
			for w := 320; w <= 1440; w++ {
				perRow, inside, usual := acceptLayout(float64(w))
				size := math.Min(usual, inside/x.Ems)
				if need*size > inside+1e-6 {
					t.Errorf("%s: at %d px, %d tiles to a row, the %s tile's %q takes %.1f px of the %.1f inside it", c.name, w, perRow, x.Key, x.Shown, need*size, inside)
					break
				}
				if c.today && size < 30-1e-9 {
					t.Errorf("%s: at %d px, %d tiles to a row, the %s tile's %q is set at %.1f px; want 30 px or more", c.name, w, perRow, x.Key, x.Shown, size)
					break
				}
			}
		}
	}
}

// The snapshot carries the tiles of its totals, and each repository the tiles
// of its own, which the page shows when that repository is chosen.
func TestEachRepositoryShowsItsOwnTiles(t *testing.T) {
	s := &Server{Repos: []*Repo{{Name: "gitdek/invariant"}, {Name: "gitdek/copythis-ad"}}}
	s.Repos[0].src.log = "| ID | Date | Door | Status | Decision | Who |\n| :-- | :-- | :-- | :-- | :-- | :-- |\n" +
		"| D-0001 | 2026-09-25 | two-way | ratified | The store is SQLite. | @gitdek |\n" +
		"| D-0002 | 2026-09-26 | two-way | decided | Journal every write. | agent |\n"
	snap := s.assemble(time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC))
	if want := Tiles(snap.Totals, snap.Who); len(snap.Tiles) != 6 || !reflect.DeepEqual(snap.Tiles, want) {
		t.Errorf("the snapshot's tiles are %+v; want its totals' %+v", snap.Tiles, want)
	}
	if len(snap.Repos) != 2 {
		t.Fatalf("the snapshot has %d repositories; want 2", len(snap.Repos))
	}
	for _, rs := range snap.Repos {
		if want := Tiles(rs.Totals, snap.Who); !reflect.DeepEqual(rs.Tiles, want) {
			t.Errorf("%s's tiles are %+v; want its own totals' %+v", rs.Name, rs.Tiles, want)
		}
	}
	if a, b := acceptTile(t, snap.Repos[0].Tiles, "dec"), acceptTile(t, snap.Repos[1].Tiles, "dec"); a.Shown != "2" || b.Shown != "0" {
		t.Errorf("the decisions tile shows %q for gitdek/invariant and %q for gitdek/copythis-ad; want 2 and 0", a.Shown, b.Shown)
	}
}

// The page reads every field of a tile, and the tiles of the snapshot and of
// each repository.
func TestThePageReadsEveryTileField(t *testing.T) {
	b, err := web.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	rt := reflect.TypeOf(Tile{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" && !strings.Contains(js, "."+tag) {
			t.Errorf("the page never reads Tile.%s", tag)
		}
	}
	for _, v := range []any{Snapshot{}, RepoState{}} {
		f, ok := reflect.TypeOf(v).FieldByName("Tiles")
		if !ok {
			t.Errorf("%T has no Tiles", v)
			continue
		}
		if tag := strings.Split(f.Tag.Get("json"), ",")[0]; tag == "" || tag == "-" || !strings.Contains(js, "."+tag) {
			t.Errorf("the page never reads %T's tiles, sent as %q", v, tag)
		}
	}
}
