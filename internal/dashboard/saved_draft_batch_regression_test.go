package dashboard

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Run the embedded banner renderer with the confirmation already open when
// a snapshot loses saved evidence. This needs no browser or DOM substitute.
func TestSavedDraftPendingBatchLosesUnresolvedEvidence(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("batch renderer check needs Node.js")
	}
	script, err := web.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	_, renderer, ok := strings.Cut(string(script), "  const batch = ")
	if !ok {
		t.Fatal("batch renderer missing from app.js")
	}
	renderer, _, ok = strings.Cut(renderer, "  // ---------- acting:")
	if !ok {
		t.Fatal("end of batch renderer missing from app.js")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(`
const assert = require("node:assert/strict");
const ACT = true, esc = String;
const ref = (repo, issue) => repo + "#" + issue;
const acts = new Map();
const actOf = (key) => acts.get(key) || {};
const batch = ` + renderer + savedDraftBatchCheck)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pending saved-draft batch: %v\n%s", err, out)
	}
}

const savedDraftBatchCheck = `
const proposal = (number) => ({repo: "owner/saved", number, waiting: {
  kind: "proposal", name: "Proposal " + number, hash: "hash-" + number, together: true,
  savedDraft: {locator: "/draft/owner/saved/" + number, problem: ""}
}});
const ask = () => {
  batch.error = "";
  batch.confirm = batch.shown.map((i) => ({repo: i.repo, issue: i.number, name: i.waiting.name, hash: i.waiting.hash}));
};
const canConfirm = (html) => html.includes('data-batch="post"');
for (const siblings of [1, 2]) {
  Object.assign(batch, {shown: [], confirm: null, busy: false, error: ""});
  const items = Array.from({length: siblings + 1}, (_, n) => proposal(897 + n));
  // A legacy chart-free proposal remains eligible alongside saved drafts.
  delete items[1].waiting.savedDraft;
  assert(batchBanner(items).includes('data-batch="ask"'), "healthy batch cannot be selected");
  ask();
  assert(canConfirm(batchBanner(items)), "healthy selection cannot be confirmed");
  const selected = JSON.stringify(batch.confirm);
  assert(canConfirm(batchBanner(structuredClone(items))), "unchanged refresh discarded confirmation");
  assert.equal(JSON.stringify(batch.confirm), selected, "unchanged refresh changed selection");

  const healthy = structuredClone(items[0].waiting);
  Object.assign(items[0].waiting, {hash: "", together: false, takes: ["revise"]});
  items[0].waiting.savedDraft.problem = "Saved result is unavailable; retrying.";
  const unavailable = batchBanner(items);
  assert(!canConfirm(unavailable), "unresolved saved evidence still offers bulk confirmation");
  assert.equal(batch.confirm, null, "unresolved proposal retained the pending selection");
  assert.deepEqual(batch.shown.map((i) => i.number), items.slice(1).map((i) => i.number), "healthy siblings lost eligibility");
  assert(unavailable.includes('class="acterr"'), "cancelled confirmation has no explanation");
  assert.equal(unavailable.includes('data-batch="ask"'), siblings > 1, "remaining batch has the wrong controls");
  if (siblings > 1) {
    ask();
    assert(canConfirm(batchBanner(items)), "healthy siblings cannot be confirmed afresh");
    assert.deepEqual(batch.confirm.map((i) => i.issue), [898, 899], "fresh selection contains unresolved evidence");
    batch.confirm = null;
  }

  items[0].waiting = healthy;
  assert(!canConfirm(batchBanner(items)), "recovery revived a stale confirmation");
  ask();
  assert(canConfirm(batchBanner(items)), "recovered evidence cannot be confirmed afresh");
  assert.equal(JSON.stringify(batch.confirm), selected, "recovery changed the ratification hashes");
  // The diagnostic itself must exclude the reference, even if an old
  // eligibility field survives in a client snapshot.
  items[0].waiting.savedDraft.problem = "Saved result is unavailable; retrying.";
  assert(!canConfirm(batchBanner(items)), "diagnostic left a saved reference in the pending batch");
}

// Revisions must not reuse the confirmation of a different proposal hash.
const revised = [proposal(897), proposal(898)];
batchBanner(revised);
ask();
revised[0].waiting.hash = "revised-hash";
assert(!canConfirm(batchBanner(revised)), "revision retained a confirmation for its previous hash");

// A refresh during a POST must not enable another batch while it is in flight.
const sending = [proposal(897), proposal(898), proposal(899)];
batchBanner(sending);
ask();
batch.busy = true;
sending[0].waiting.savedDraft.problem = "Saved result is unavailable; retrying.";
const pending = batchBanner(sending);
assert(!canConfirm(pending), "in-flight refresh kept unresolved confirmation");
assert(pending.includes('data-batch="ask" disabled'), "in-flight refresh enabled another batch");
`
