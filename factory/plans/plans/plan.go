// +gobra

package plans

// An issue's status, as the issue protocol reports it.
const (
	Open   = 1
	Failed = 2
	Merged = 3
)

// MaxCapacity keeps issue counts far from overflow. It's the machine's limit, not the
// model's.
const MaxCapacity = 1 << 30

// Plan is a ratified plan's run as GitHub shows it: who ratified it, whether it was
// stopped and when, the issues the factory created for it, which of them the plan's
// issue records, whether the factory said the plan is done, and who holds the lease.
// Issues are created in the plan's order, so issue k+1 is the one for step k+1.
type Plan struct {
	Ratifier int    // 0 until ratified, then the ratifier
	StopMark int    // -1 until stopped, then how many issues existed when it was
	Count    int    // how many issues were created
	Status   []int  // Status[k] is issue k+1's status
	Author   []int  // Author[k] is whose authority issue k+1 is solved on
	Recorded []bool // Recorded[k] once the plan's issue records issue k+1
	Done     bool   // the plan's issue says the plan is done
	Leader   int    // 0 when no watcher holds the lease, else the watcher
}

// @ requires acc(&pl.Count, _) && acc(&pl.Status, _) && acc(&pl.Author, _) && acc(&pl.Recorded, _)
// @ decreases
// @ pure
func (pl *Plan) Ok() bool {
	return 0 < len(pl.Status) && len(pl.Status) <= MaxCapacity && len(pl.Author) == len(pl.Status) &&
		len(pl.Recorded) == len(pl.Status) && 0 <= pl.Count && pl.Count <= len(pl.Status)
}

// New is an unratified plan of n steps, with no issue created yet.
// @ requires 0 < n && n <= MaxCapacity
// @ ensures acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ ensures acc(&pl.Status) && acc(&pl.Author) && acc(&pl.Recorded) && pl.Ok() && len(pl.Status) == n
// @ ensures forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ ensures forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ ensures forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures pl.Ratifier == 0 && pl.StopMark == -1 && pl.Count == 0 && !pl.Done && pl.Leader == 0
// @ ensures forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) ==> pl.Status[j] == 0
// @ ensures forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) ==> pl.Author[j] == 0
// @ ensures forall j int :: { pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> !pl.Recorded[j]
func New(n int) (pl *Plan) {
	pl = &Plan{StopMark: -1, Status: make([]int, n), Author: make([]int, n), Recorded: make([]bool, n)}
	return pl
}

// Ratify has a person ratify the plan, unless it's already ratified or stopped.
// @ requires acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ requires acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ requires forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ requires forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ requires forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ requires 0 < who
// @ ensures acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ ensures acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ ensures len(pl.Status) == old(len(pl.Status)) && len(pl.Author) == old(len(pl.Author)) && len(pl.Recorded) == old(len(pl.Recorded))
// @ ensures forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ ensures forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ ensures forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures ok == old(pl.Ratifier == 0 && pl.StopMark == -1)
// @ ensures ok ==> pl.Ratifier == who
// @ ensures !ok ==> pl.Ratifier == old(pl.Ratifier)
// @ ensures pl.StopMark == old(pl.StopMark) && pl.Count == old(pl.Count) && pl.Done == old(pl.Done) && pl.Leader == old(pl.Leader)
// @ ensures forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) ==> pl.Status[j] == old(pl.Status[j])
// @ ensures forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) ==> pl.Author[j] == old(pl.Author[j])
// @ ensures forall j int :: { pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> pl.Recorded[j] == old(pl.Recorded[j])
func (pl *Plan) Ratify(who int) (ok bool) {
	if pl.Ratifier != 0 || pl.StopMark != -1 {
		return false
	}
	pl.Ratifier = who
	return true
}

// Stop has a person stop the plan, unless it's already stopped or done. It marks how
// many issues exist, and none is created after.
// @ requires acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ requires acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ requires forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ requires forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ requires forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ ensures acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ ensures len(pl.Status) == old(len(pl.Status)) && len(pl.Author) == old(len(pl.Author)) && len(pl.Recorded) == old(len(pl.Recorded))
// @ ensures forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ ensures forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ ensures forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures ok == old(pl.StopMark == -1 && !pl.Done)
// @ ensures ok ==> pl.StopMark == old(pl.Count)
// @ ensures !ok ==> pl.StopMark == old(pl.StopMark)
// @ ensures pl.Ratifier == old(pl.Ratifier) && pl.Count == old(pl.Count) && pl.Done == old(pl.Done) && pl.Leader == old(pl.Leader)
// @ ensures forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) ==> pl.Status[j] == old(pl.Status[j])
// @ ensures forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) ==> pl.Author[j] == old(pl.Author[j])
// @ ensures forall j int :: { pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> pl.Recorded[j] == old(pl.Recorded[j])
func (pl *Plan) Stop() (ok bool) {
	if pl.StopMark != -1 || pl.Done {
		return false
	}
	pl.StopMark = pl.Count
	return true
}

// Acquire has watcher w take the lease, unless someone holds it.
// @ requires acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ requires acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ requires forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ requires forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ requires forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ requires 0 < w
// @ ensures acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ ensures acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ ensures len(pl.Status) == old(len(pl.Status)) && len(pl.Author) == old(len(pl.Author)) && len(pl.Recorded) == old(len(pl.Recorded))
// @ ensures forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ ensures forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ ensures forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures ok == old(pl.Leader == 0)
// @ ensures ok ==> pl.Leader == w
// @ ensures !ok ==> pl.Leader == old(pl.Leader)
// @ ensures pl.Ratifier == old(pl.Ratifier) && pl.StopMark == old(pl.StopMark) && pl.Count == old(pl.Count) && pl.Done == old(pl.Done)
// @ ensures forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) ==> pl.Status[j] == old(pl.Status[j])
// @ ensures forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) ==> pl.Author[j] == old(pl.Author[j])
// @ ensures forall j int :: { pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> pl.Recorded[j] == old(pl.Recorded[j])
func (pl *Plan) Acquire(w int) (ok bool) {
	if pl.Leader != 0 {
		return false
	}
	pl.Leader = w
	return true
}

// Crash has watcher w, holding the lease, crash, and its lease run out.
// @ requires acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ requires acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ requires forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ requires forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ requires forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ requires 0 < w
// @ ensures acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ ensures acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ ensures len(pl.Status) == old(len(pl.Status)) && len(pl.Author) == old(len(pl.Author)) && len(pl.Recorded) == old(len(pl.Recorded))
// @ ensures forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ ensures forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ ensures forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures ok == old(pl.Leader == w)
// @ ensures ok ==> pl.Leader == 0
// @ ensures !ok ==> pl.Leader == old(pl.Leader)
// @ ensures pl.Ratifier == old(pl.Ratifier) && pl.StopMark == old(pl.StopMark) && pl.Count == old(pl.Count) && pl.Done == old(pl.Done)
// @ ensures forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) ==> pl.Status[j] == old(pl.Status[j])
// @ ensures forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) ==> pl.Author[j] == old(pl.Author[j])
// @ ensures forall j int :: { pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> pl.Recorded[j] == old(pl.Recorded[j])
func (pl *Plan) Crash(w int) (ok bool) {
	if pl.Leader != w {
		return false
	}
	pl.Leader = 0
	return true
}

// Create is the first effect of opening: watcher w, holding the lease, looks on GitHub
// and creates the next step's issue, on the ratifier's authority, only if the plan is
// ratified and not stopped, every issue created so far has merged, and a step is left.
// @ requires acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ requires acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ requires forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ requires forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ requires forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ requires 0 < w
// @ ensures acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ ensures acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ ensures len(pl.Status) == old(len(pl.Status)) && len(pl.Author) == old(len(pl.Author)) && len(pl.Recorded) == old(len(pl.Recorded))
// @ ensures forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ ensures forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ ensures forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures ok == old(pl.Leader == w && pl.Ratifier != 0 && pl.StopMark == -1 && pl.Count < len(pl.Status) && (pl.Count == 0 || pl.Status[pl.Count-1] == Merged))
// @ ensures ok ==> pl.Count == old(pl.Count) + 1 && pl.Status[old(pl.Count)] == Open && pl.Author[old(pl.Count)] == pl.Ratifier
// @ ensures !ok ==> pl.Count == old(pl.Count)
// @ ensures forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) && j != old(pl.Count) ==> pl.Status[j] == old(pl.Status[j])
// @ ensures forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) && j != old(pl.Count) ==> pl.Author[j] == old(pl.Author[j])
// @ ensures !ok ==> forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) ==> pl.Status[j] == old(pl.Status[j])
// @ ensures !ok ==> forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) ==> pl.Author[j] == old(pl.Author[j])
// @ ensures pl.Ratifier == old(pl.Ratifier) && pl.StopMark == old(pl.StopMark) && pl.Done == old(pl.Done) && pl.Leader == old(pl.Leader)
// @ ensures forall j int :: { pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> pl.Recorded[j] == old(pl.Recorded[j])
func (pl *Plan) Create(w int) (ok bool) {
	if pl.Leader != w || pl.Ratifier == 0 || pl.StopMark != -1 || pl.Count == len(pl.Status) {
		return false
	}
	if pl.Count > 0 {
		last := pl.Count - 1
		if pl.Status[last] != Merged {
			return false
		}
	}
	pl.Status[pl.Count] = Open
	pl.Author[pl.Count] = pl.Ratifier
	pl.Count = pl.Count + 1
	return true
}

// Record is the second effect of opening: watcher w, holding the lease, posts on the
// plan's issue that issue i is open, if it was created and isn't recorded yet.
// @ requires acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ requires acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ requires forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ requires forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ requires forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ requires 0 < w && 1 <= i && i <= len(pl.Recorded)
// @ ensures acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ ensures acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ ensures len(pl.Status) == old(len(pl.Status)) && len(pl.Author) == old(len(pl.Author)) && len(pl.Recorded) == old(len(pl.Recorded))
// @ ensures forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ ensures forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ ensures forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures ok == old(pl.Leader == w && i <= pl.Count && !pl.Recorded[i-1])
// @ ensures ok ==> pl.Recorded[i-1]
// @ ensures !ok ==> pl.Recorded[i-1] == old(pl.Recorded[i-1])
// @ ensures forall j int :: { pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) && j != i-1 ==> pl.Recorded[j] == old(pl.Recorded[j])
// @ ensures pl.Ratifier == old(pl.Ratifier) && pl.StopMark == old(pl.StopMark) && pl.Count == old(pl.Count) && pl.Done == old(pl.Done) && pl.Leader == old(pl.Leader)
// @ ensures forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) ==> pl.Status[j] == old(pl.Status[j])
// @ ensures forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) ==> pl.Author[j] == old(pl.Author[j])
func (pl *Plan) Record(w, i int) (ok bool) {
	if pl.Leader != w || pl.Count < i || pl.Recorded[i-1] {
		return false
	}
	pl.Recorded[i-1] = true
	return true
}

// Finish has watcher w, holding the lease, say on the plan's issue that the plan is
// done, once every step's issue was created and the last of them has merged.
// @ requires acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ requires acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ requires forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ requires forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ requires forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ requires 0 < w
// @ ensures acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ ensures acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ ensures len(pl.Status) == old(len(pl.Status)) && len(pl.Author) == old(len(pl.Author)) && len(pl.Recorded) == old(len(pl.Recorded))
// @ ensures forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ ensures forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ ensures forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures ok == old(pl.Leader == w && pl.Ratifier != 0 && !pl.Done && pl.Count == len(pl.Status) && pl.Status[pl.Count-1] == Merged)
// @ ensures ok ==> pl.Done
// @ ensures !ok ==> pl.Done == old(pl.Done)
// @ ensures pl.Ratifier == old(pl.Ratifier) && pl.StopMark == old(pl.StopMark) && pl.Count == old(pl.Count) && pl.Leader == old(pl.Leader)
// @ ensures forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) ==> pl.Status[j] == old(pl.Status[j])
// @ ensures forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) ==> pl.Author[j] == old(pl.Author[j])
// @ ensures forall j int :: { pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> pl.Recorded[j] == old(pl.Recorded[j])
func (pl *Plan) Finish(w int) (ok bool) {
	if pl.Leader != w || pl.Ratifier == 0 || pl.Done || pl.Count != len(pl.Status) {
		return false
	}
	if pl.Status[pl.Count-1] != Merged {
		return false
	}
	pl.Done = true
	return true
}

// Fail has issue i, created and open, fail in the issue protocol.
// @ requires acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ requires acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ requires forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ requires forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ requires forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ requires 1 <= i && i <= len(pl.Status)
// @ ensures acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ ensures acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ ensures len(pl.Status) == old(len(pl.Status)) && len(pl.Author) == old(len(pl.Author)) && len(pl.Recorded) == old(len(pl.Recorded))
// @ ensures forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ ensures forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ ensures forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures ok == old(i <= pl.Count && pl.Status[i-1] == Open)
// @ ensures ok ==> pl.Status[i-1] == Failed
// @ ensures !ok ==> pl.Status[i-1] == old(pl.Status[i-1])
// @ ensures forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) && j != i-1 ==> pl.Status[j] == old(pl.Status[j])
// @ ensures pl.Ratifier == old(pl.Ratifier) && pl.StopMark == old(pl.StopMark) && pl.Count == old(pl.Count) && pl.Done == old(pl.Done) && pl.Leader == old(pl.Leader)
// @ ensures forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) ==> pl.Author[j] == old(pl.Author[j])
// @ ensures forall j int :: { pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> pl.Recorded[j] == old(pl.Recorded[j])
func (pl *Plan) Fail(i int) (ok bool) {
	if pl.Count < i || pl.Status[i-1] != Open {
		return false
	}
	pl.Status[i-1] = Failed
	return true
}

// Merge has issue i, created and open or failed, merge in the issue protocol.
// @ requires acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ requires acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ requires forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ requires forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ requires forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ requires 1 <= i && i <= len(pl.Status)
// @ ensures acc(&pl.Ratifier) && acc(&pl.StopMark) && acc(&pl.Count) && acc(&pl.Done) && acc(&pl.Leader)
// @ ensures acc(&pl.Status, 1/2) && acc(&pl.Author, 1/2) && acc(&pl.Recorded, 1/2) && pl.Ok()
// @ ensures len(pl.Status) == old(len(pl.Status)) && len(pl.Author) == old(len(pl.Author)) && len(pl.Recorded) == old(len(pl.Recorded))
// @ ensures forall j int :: { &pl.Status[j] } 0 <= j && j < len(pl.Status) ==> acc(&pl.Status[j])
// @ ensures forall j int :: { &pl.Author[j] } 0 <= j && j < len(pl.Author) ==> acc(&pl.Author[j])
// @ ensures forall j int :: { &pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> acc(&pl.Recorded[j])
// @ ensures ok == old(i <= pl.Count && (pl.Status[i-1] == Open || pl.Status[i-1] == Failed))
// @ ensures ok ==> pl.Status[i-1] == Merged
// @ ensures !ok ==> pl.Status[i-1] == old(pl.Status[i-1])
// @ ensures forall j int :: { pl.Status[j] } 0 <= j && j < len(pl.Status) && j != i-1 ==> pl.Status[j] == old(pl.Status[j])
// @ ensures pl.Ratifier == old(pl.Ratifier) && pl.StopMark == old(pl.StopMark) && pl.Count == old(pl.Count) && pl.Done == old(pl.Done) && pl.Leader == old(pl.Leader)
// @ ensures forall j int :: { pl.Author[j] } 0 <= j && j < len(pl.Author) ==> pl.Author[j] == old(pl.Author[j])
// @ ensures forall j int :: { pl.Recorded[j] } 0 <= j && j < len(pl.Recorded) ==> pl.Recorded[j] == old(pl.Recorded[j])
func (pl *Plan) Merge(i int) (ok bool) {
	if pl.Count < i || (pl.Status[i-1] != Open && pl.Status[i-1] != Failed) {
		return false
	}
	pl.Status[i-1] = Merged
	return true
}
