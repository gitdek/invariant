package plumbing

import (
	"fmt"
	"strings"
)

// RepositoryAcceptanceBinding binds every record to the host's fresh attempt,
// exact identity, package, verbatim source bytes and captured configuration.
// It is a value so the environment cannot mutate the host's expected binding.
type RepositoryAcceptanceBinding struct {
	InvocationID    string
	Part            string
	Framework       string
	File            string
	Selector        string
	Package         string
	SourceDigest    string
	ExecutionDigest string
}

type RepositoryAcceptanceEvidence struct {
	Complete bool
	ExitCode int
	Records  []RepositoryAcceptanceRecord
}

type RepositoryAcceptanceRecord struct {
	Binding RepositoryAcceptanceBinding
	Kind    string
	Status  string
	Reason  string
}

// repositoryEvidence consumes only the environment's evidence channel. Neither
// an exit code alone nor any text in diagnostics establishes a test verdict.
func repositoryEvidence(expected RepositoryAcceptanceBinding, evidence RepositoryAcceptanceEvidence, result TestResult) (TestResult, error) {
	return repositoryEvidenceMode(expected, evidence, result, false)
}

// Real Go evidence has already passed CollectGoAcceptance. Its authentic
// prefix survives an infrastructure veto, but completion still needs a start.
// Trusted RunAcceptance environments retain their original terminal contract.
func repositoryEvidenceMode(expected RepositoryAcceptanceBinding, evidence RepositoryAcceptanceEvidence, result TestResult, goRuntime bool) (TestResult, error) {
	result.Package = expected.Package
	refuse := func(reason string) (TestResult, error) {
		result.Kind, result.Status, result.Reason = "evidence", "refused", reason
		return result, fmt.Errorf("accept %s: %s", result.identity(), reason)
	}
	for _, record := range evidence.Records {
		if record.Binding != expected {
			return refuse("evidence binding does not match this invocation, identity, package, source and configuration")
		}
	}
	if len(evidence.Records) == 0 {
		result.Kind, result.Status, result.Reason = "evidence", "not_run", "no selection or terminal evidence was received"
		return result, fmt.Errorf("accept %s: %s", result.identity(), result.Reason)
	}
	// Discovery can fail before selection. Preserve its explanation without
	// promoting a setup failure or refusal to a completed test failure.
	first := evidence.Records[0]
	if len(evidence.Records) == 1 && (first.Kind == "setup" && first.Status == "failed" || first.Kind == "test" && first.Status == "refused") {
		result.Kind, result.Status, result.Reason = first.Kind, "refused", acceptanceReason(first)
		return result, fmt.Errorf("accept %s: %s", result.identity(), result.Reason)
	}
	if first.Kind != "selection" || first.Status != "" {
		return refuse("evidence must begin with exactly one selection record")
	}
	var terminal *RepositoryAcceptanceRecord
	var running *RepositoryAcceptanceRecord
	for i := 1; i < len(evidence.Records); i++ {
		record := &evidence.Records[i]
		if terminal != nil {
			return refuse("duplicate or contradictory records after terminal evidence")
		}
		if record.Kind == "test" && record.Status == "running" {
			if running != nil {
				return refuse("duplicate running evidence")
			}
			running = record
			continue
		}
		switch record.Kind {
		case "test":
			switch record.Status {
			case "passed", "failed", "skipped", "refused":
			default:
				return refuse("unknown test evidence status")
			}
		case "compile", "setup":
			if record.Status != "failed" {
				return refuse("compiler and setup evidence must report failure")
			}
		default:
			return refuse("unknown or duplicate evidence record")
		}
		terminal = record
	}
	if terminal == nil {
		if running != nil {
			result.Kind, result.Status, result.Reason = "test", "running", acceptanceReason(*running)
		} else {
			result.Kind, result.Status, result.Reason = "evidence", "not_run", "selection has no terminal evidence"
		}
		return result, fmt.Errorf("accept %s: %s", result.identity(), result.Reason)
	}
	if terminal.Kind == "setup" {
		result.Kind, result.Status, result.Reason = "setup", "refused", acceptanceReason(*terminal)
		return result, fmt.Errorf("accept %s: %s", result.identity(), result.Reason)
	}
	if goRuntime {
		if terminal.Kind != "test" || running == nil {
			return refuse("Go test completion needs authentic running evidence")
		}
		result.Kind, result.Status, result.Reason = terminal.Kind, terminal.Status, acceptanceReason(*terminal)
	}
	veto := func(reason string) (TestResult, error) {
		if goRuntime {
			return result, fmt.Errorf("accept %s: %s", result.identity(), reason)
		}
		return refuse(reason)
	}
	// Process and channel failures veto qualification. Only the real collector's
	// authenticated terminal may survive them as completed work in the report.
	if evidence.ExitCode < 0 {
		return veto("evidence has no completed process exit: " + acceptanceReason(*terminal))
	}
	if !evidence.Complete {
		return veto("evidence channel did not complete: " + acceptanceReason(*terminal))
	}
	if evidence.ExitCode != 0 && terminal.Status != "failed" {
		return veto("nonzero exit without attributable test or compiler failure: " + acceptanceReason(*terminal))
	}
	result.Kind, result.Status, result.Reason = terminal.Kind, terminal.Status, acceptanceReason(*terminal)
	return result, nil
}

func acceptanceReason(record RepositoryAcceptanceRecord) string {
	if strings.TrimSpace(record.Reason) != "" {
		return record.Reason
	}
	return record.Kind + " " + record.Status
}
