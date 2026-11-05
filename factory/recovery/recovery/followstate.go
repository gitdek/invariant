// +gobra

package recovery

// The follow-up journal's state. Gobra proves the transaction core that
// reads and writes it (this file, journal.go and recovery.go). The
// follow-up logic in followup.go is plain Go, checked against the model by
// conformance.

// FollowIdentity belongs to a ratified-plan obligation, not to an attempt.
// Drafts, creation attempts, repairs, reopening and merging allocate separate
// operation identities while retaining this identity in every accepted snapshot.
type FollowIdentity struct{ Source, Branch, PR string }

// FollowEffect is a durable intent and its reconciliation state. Dispatch is
// recorded once; a lost reply leaves it pending instead of enabling a resend.
type FollowEffect struct {
	Intent     int
	Dispatched bool
	State      string // empty, pending, completed or cancelled
}

type FollowOperation struct {
	Kind                            string
	Base, Round                     int
	Raw, Result                     string
	ResultSnapshot                  int
	Run, Publication, Merge, Reopen FollowEffect
}

type FollowSnapshot struct {
	Identity          FollowIdentity
	Op                int
	Kind              string
	Head, Left, Round int
	Cause, Stage      string
}

// FollowControl is the durable Workflow cursor. Remote facts and effect counts
// are not stored here: callers supply observations, and the explorer keeps the
// environment and audit history. All transitions are fenced by FollowSession.
type FollowControl struct {
	Identity                           FollowIdentity
	Phase, Next                        string
	Op, N, Left, Round                 int
	Requested                          bool
	Consumed                           int
	Answer                             FollowEffect
	Owner, RunFence                    int
	KnownPR                            bool
	Head                               int
	CI                                 string
	CIHead                             int
	CanMerge                           bool
	MergeHead                          int
	MergeOutcome, Proof, Status, Cause string
}

// FollowChange is prepared from a cursor and committed to the journal before
// an adapter performs its newly dispatched effect. An empty snapshot denotes
// a ledger-only transition. Effect identities are (Op, effect kind).
type FollowChange struct {
	Before    FollowControl
	Previous  FollowOperation
	Control   FollowControl
	Operation FollowOperation
	Snapshot  FollowSnapshot
	OK        bool
}

// @ ensures c.Identity == identity && c.Phase == "inactive" && c.Next == "draft"
// @ ensures c.Op == 0 && c.N == 0 && c.Left == 2 && c.Round == 0
// @ ensures !c.Requested && c.Consumed == 0 && c.Answer == (FollowEffect{})
// @ ensures c.Owner == NoOne && c.RunFence == 0 && !c.KnownPR && c.Head == 0
// @ ensures c.CI == "none" && c.CIHead == 0 && !c.CanMerge && c.MergeHead == 0
// @ ensures c.MergeOutcome == "none" && c.Proof == "none" && c.Status == "active" && c.Cause == "none"
func NewFollowControl(identity FollowIdentity) (c FollowControl) {
	c.Identity = identity
	c.Phase, c.Next = "inactive", "draft"
	c.Left, c.Owner = 2, NoOne
	c.CI, c.MergeOutcome, c.Proof, c.Status, c.Cause = "none", "none", "none", "active", "none"
	return c
}

// @ decreases
// @ pure
func (c FollowControl) Valid() bool {
	return 0 <= c.Op && c.Op <= MaxAttempt && 0 <= c.N && c.N <= MaxAttempt &&
		0 <= c.Left && c.Left <= 2 && 0 <= c.Round && c.Round < MaxAttempt
}
