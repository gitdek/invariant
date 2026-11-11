package formalize

import (
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
)

func TestTypeScriptNewModulePlanningGuidance788(t *testing.T) {
	req := Request{Repo: "example/frontend", Issue: 788, Title: "Add a new TypeScript module", Author: "owner"}
	for _, configured := range []bool{false, true} {
		for _, revision := range []bool{false, true} {
			current := req
			if revision {
				current.Previous = &Proposal{Plan: &plumbing.Plan{Name: "Earlier module plan"}}
			}
			prompt := planPrompt(current, 4, configured)
			lower := strings.ToLower(prompt)
			for _, term := range []string{"relative", "path aliases", "string", "locally", "assertion", "ts2305", "module", "creates", "vitest", "@jest/globals"} {
				if !strings.Contains(lower, term) {
					t.Errorf("configured=%v revision=%v: new-module guidance lacks %q", configured, revision, term)
				}
			}
			if !strings.Contains(prompt, "expect(api()).matcher(literal)") {
				t.Error("guidance must distinguish the narrow missing-export attribution case from a new module's runtime assertion")
			}
			// Check the user-facing example's ingredients without pinning its
			// local names, formatting, business API or surrounding prose.
			blocks := regexp.MustCompile("(?s)```(?:typescript|ts)\\s*\\n(.*?)```").FindAllStringSubmatch(prompt, -1)
			found := false
			for _, block := range blocks {
				source := block[1]
				path := regexp.MustCompile(`(?:const|let)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*:\s*string\s*=\s*["']\.[^"']*["']`).FindStringSubmatch(source)
				if path == nil || !regexp.MustCompile(`import\(\s*`+regexp.QuoteMeta(path[1])+`\s*\)`).MatchString(source) {
					continue
				}
				localType := regexp.MustCompile(`(?s)(?:type\s+\w+\s*=\s*\{|interface\s+\w+\s*\{|(?:const|let)\s+\w+\s*:\s*\{)`).MatchString(source)
				catchesLoad := strings.Contains(source, ".catch(") || strings.Contains(source, "catch (") || strings.Contains(source, "catch(") || strings.Contains(source, ".then(")
				if localType && catchesLoad && strings.Contains(source, "async") && strings.Contains(source, "await") &&
					regexp.MustCompile(`(?:test|it)\s*\(`).MatchString(source) && strings.Count(source, "expect(") >= 2 &&
					strings.Contains(source, "vitest") && !strings.Contains(source, "@/") {
					found = true
				}
			}
			if !found {
				t.Errorf("configured=%v revision=%v: no short TypeScript example loads a relative string specifier, types the new API locally, and asserts loading and behavior inside an async test", configured, revision)
			}
			for _, kept := range []string{"You have 4 checks", "every correction needs a runtime pass", "conservative analysis"} {
				if !strings.Contains(prompt, kept) {
					t.Errorf("existing planning requirement disappeared: %q", kept)
				}
			}
		}
	}
}
