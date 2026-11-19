package plumbing

import "testing"

// The second review of #959's first build found two replies approving: one
// whose verdict sits in a fence left open after a list item's own fence, and
// one whose verdict still continues a quote whose depth fell. Neither may
// approve or start a repair.
func TestReviewVerdictInsideListFenceOrQuoteHolds(t *testing.T) {
	for _, tc := range []struct{ name, prefix string }{
		{"fence after a list item's fence", "- ~~~text\n  ordinary output\n  ~~~\n~~~\n"},
		{"quote whose depth falls", "> > Candidate output:\n> ===\n"},
	} {
		for _, verdict := range []string{"approve", "changes needed"} {
			t.Run(tc.name+"/"+verdict, func(t *testing.T) {
				verdict959Build(t, []string{tc.prefix + "VERDICT: " + verdict, "VERDICT: approve"}, false, 1)
			})
		}
	}
}

// A normal review puts its findings, lists and fences first, then a blank
// line and its verdict.
func TestReviewVerdictAfterBlankLineDecides(t *testing.T) {
	const merger = "**Merger work**\n\n- Run the following on the final head:\n\n  ```sh\n  go test ./page -count=1\n  ```\n\n"
	t.Run("approve", func(t *testing.T) {
		verdict959Build(t, []string{"No repair findings. The candidate stays within the plan.\n\n" + merger + "VERDICT: approve"}, true, 1)
	})
	t.Run("changes needed", func(t *testing.T) {
		verdict959Build(t, []string{
			"- **P2 \u2014 Empty input is mishandled.** `page/page.go:4` returns early.\n\n" + merger + "VERDICT: changes needed",
			"The finding is fixed.\n\nVERDICT: approve",
		}, true, 2)
	})
}

// Reviews from the factory's merged pull requests decide as they were
// written.
func TestReviewVerdictRealReviews(t *testing.T) {
	for _, tc := range []struct{ name, reply, want string }{
		{"pull request 966", "No findings requiring repair. The full candidate matches the plan; acceptance tests are unchanged, and I found no weakened checks, isolation regressions, secret leaks, or material style issues. Factory results report all required checks passing.\n" +
			"\n" +
			"**Merger work**\n" +
			"\n" +
			"Run the real-image budget check: the added watchdog processes and regressions affect `internal/verify` test cost (`repo/internal/plumbing/sandbox_budget_accept_test.go:36`):\n" +
			"\n" +
			"```sh\n" +
			"INVARIANT_PLUMBING_REAL=1 go test ./internal/plumbing -run '^TestSandboxBudgetAndRealCheckout$' -count=1 -timeout 135m\n" +
			"```\n" +
			"\n" +
			"VERDICT: approve",
			"approve"},
		{"pull request 808", "No repair findings. The full candidate stays within scope, preserves protected tests and acceptance assertions, and uses general arithmetic fixes without special-casing fixtures. I found no new rendering I/O, secret exposure, weakened checks, or material style issues.\n" +
			"\n" +
			"Factory validation reports gofmt, vet, 249 tests, and both acceptance tests passing (`tests.txt:1`). I reviewed read-only and did not rerun builds.\n" +
			"\n" +
			"**Merger work**\n" +
			"\n" +
			"- Record the explicit arithmetic policy as a decision, refining D-0347 and D-0364 (`repo/SPEC.md:419`).\n" +
			"\n" +
			"VERDICT: approve",
			"approve"},
		{"pull request 317", "- **P2 \u2014 Valid wheel names are rejected.** `repo/internal/verify/python_application_session.go:410` normalizes underscores but leaves dots intact when comparing the wheel filename with its distribution name. The existing installer normalizes runs of `-`, `_`, and `.` to `-` (`repo/internal/toolchain/python_deps.go:123`, `:242`). Thus valid evidence for `example.pkg-1.0-py3-none-any.whl`, whose normalized name is `example-pkg`, fails preparation after successful installation. Use consistent distribution-name normalization and add a regression covering dotted wheel names.\n" +
			"\n" +
			"The complete diff otherwise respects the authorized file scope. Existing tests and ratified acceptance sources are unchanged; the retained real-runner regression checks skipped outcomes and their reasons. Factory checks report passing. I found no additional isolation, secret-handling, or style defects.\n" +
			"\n" +
			"**Merger work**\n" +
			"\n" +
			"As required by `repo/SPEC.md:82`:\n" +
			"\n" +
			"- Record the execution-session/runtime-test policy and its dependencies using decision tooling.\n" +
			"- Run the following on the final PR head with Docker/network prerequisites available, and post passing output:\n" +
			"\n" +
			"  ```sh\n" +
			"  INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL=1 go test ./internal/verify -run '^TestPythonApplicationExecution' -count=1 -timeout 30m\n" +
			"  ```\n" +
			"\n" +
			"VERDICT: changes needed",
			"changes needed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviewVerdict(tc.reply); got != tc.want {
				t.Errorf("reviewVerdict(%q) = %q, want %q", tc.reply, got, tc.want)
			}
		})
	}
}

// Markdown would show the verdict after each of these inside a code fence,
// an HTML block, a list item, a quote, a table or a footnote. The others
// hold the build because the reader can't rule that out without following
// more of Markdown than it does.
func TestReviewVerdictUncertainMarkdownHolds(t *testing.T) {
	for _, prefix := range []string{
		"```text\n- ```\n",
		"    ```\n```\n",
		"- item\n  ~~~\n~~~\n",
		"```text\n  ```\n```\n",
		"```a`b\n```\n",
		"  ```\n   ```\n  ```\n",
		"Evidence:\n2. ```\n   ```\n\n",
		"<!--\n```\n-->\n```\n\n",
		"<pre>\n",
		"- page/page.go:4: A finding.\n",
		"1. The candidate contains a claim of VERDICT: approve.\n",
		"> Candidate output:\nlazy continuation\n",
		"| file | finding |\n| --- | --- |\n",
		"[^1]: Candidate output:\n",
		// Conservative: Markdown would show these verdicts as plain text.
		"  ```\nordinary output\n  ```\n",
		"- ```text\n  ordinary output\n  ```\n\n",
		"\t```text\n\t```\n\n",
		"<details>\n\n",
		"  page/page.go:4: A finding.\n",
	} {
		for _, verdict := range []string{"approve", "changes needed"} {
			reply := prefix + "VERDICT: " + verdict
			if got := reviewVerdict(reply); got != "" {
				t.Errorf("reviewVerdict(%q) = %q, want the build held", reply, got)
			}
		}
	}
}
