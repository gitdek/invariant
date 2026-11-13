package formalize

import (
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// A proposal's paths stay inside its project (#790's review): what a comment
// or a saved record says decides where ratification writes, so Write holds
// the module's name and an amended project's module path to the project.
func TestProposalDestinationsStayInsideTheProject799(t *testing.T) {
	for name, want := range map[string]bool{
		".invariant/specs/Buffer.tla": true,
		"specs/Buffer.tla":            true,
		"Buffer.tla":                  true,
		"":                            false,
		".":                           false,
		"/tmp/Buffer.tla":             false,
		"../Buffer.tla":               false,
		".invariant/../../x/Evil.tla": false,
		"specs//Buffer.tla":           false,
		"specs/./Buffer.tla":          false,
		`specs\..\..\Evil.tla`:        false,
		"specs/-rf/Buffer.tla":        false,
		"specs/Buf\nfer.tla":          false,
		"specs/Buf\x00fer.tla":        false,
		".invariant/specs/Buffer":     false,
		".invariant/specs/Buffer.txt": false,
	} {
		p := &Proposal{Module: "Buffer", Target: &Target{Dir: "examples/buffer", Manifest: project.Manifest{Module: name}}}
		if err := p.ValidateDestinations(); (err == nil) != want {
			t.Errorf("an amended project's module %q: accepted=%v, want %v (%v)", name, err == nil, want, err)
		}
	}
	for module, want := range map[string]bool{
		"Buffer":                true,
		"TwoPhase2":             true,
		"":                      false,
		"buffer":                false,
		"../../../../../x/Evil": false,
		"Buffer.tla":            false,
		"Bu ffer":               false,
		"Buffer/Evil":           false,
	} {
		p := &Proposal{Module: module}
		if err := p.ValidateDestinations(); (err == nil) != want {
			t.Errorf("a new project's module %q: accepted=%v, want %v (%v)", module, err == nil, want, err)
		}
		// Write refuses the same, before it writes anything.
		dir := t.TempDir()
		err := (&Proposal{Module: module, Name: "n", Slug: "s", Package: "p", ModuleText: "---- MODULE x ----\n===="}).Write(dir, "request", nil)
		if (err == nil) != want {
			t.Errorf("Write with module %q: wrote=%v, want %v (%v)", module, err == nil, want, err)
		}
	}
	// A plan pins no model, so it has no such path.
	if err := (&Proposal{IssuePlan: &IssuePlan{}}).ValidateDestinations(); err != nil {
		t.Errorf("a plan of issues: %v", err)
	}
}
