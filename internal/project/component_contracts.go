package project

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// ComponentContract declares a trusted contract (#294): the real operation a
// proof boundary calls, the declaration holding the contract it's trusted to
// keep, and the tests selected to back it. It is an assumption for tests to
// support, never a proof of the operation, and declaring it shows no call,
// no test run and no proof that used it.
type ComponentContract struct {
	ID          string                `json:"id"`
	Operation   ComponentOperation    `json:"operation"`
	Contract    ComponentRef          `json:"contract"`
	Tests       []ComponentRef        `json:"tests"`
	Invocations []ComponentInvocation `json:"invocations,omitempty"`
}

// ComponentOperation names the real operation a trusted contract describes.
// A standard_library operation is a module and symbol of the pinned Python
// runtime, builtins and maps included. A dependency operation adds the
// distribution whose resolved wheel provides it. An application operation is
// a declaration in the application's own source.
type ComponentOperation struct {
	Kind         string `json:"kind"`
	Distribution string `json:"distribution,omitempty"`
	Module       string `json:"module,omitempty"`
	Source       string `json:"source,omitempty"`
	Symbol       string `json:"symbol"`
}

// ResolvedComponentContract is a trusted contract's declaration, with
// canonical component IDs for its contract and backing tests. The operation's
// actual identity, a pinned runtime or a resolved wheel, is bound only where a
// report records one.
type ResolvedComponentContract struct {
	ID          string                        `json:"id"`
	Operation   ResolvedComponentOperation    `json:"operation"`
	Contract    string                        `json:"contract"`
	Tests       []string                      `json:"tests"`
	Invocations []ResolvedComponentInvocation `json:"invocations,omitempty"`
	// Exsures are the exception classes the contract says its operation may
	// raise, by the dotted path each of its Exsures calls resolves to; under
	// its precondition, the operation raises no other.
	Exsures []string `json:"exsures,omitempty"`
}

// ResolvedComponentOperation keeps a dependency's normalized distribution
// name, and an application operation's canonical declaration ID.
type ResolvedComponentOperation struct {
	Kind         string `json:"kind"`
	Distribution string `json:"distribution,omitempty"`
	Module       string `json:"module,omitempty"`
	Symbol       string `json:"symbol,omitempty"`
	Declaration  string `json:"declaration,omitempty"`
}

// Operation kinds.
const (
	OperationStandardLibrary = "standard_library"
	OperationDependency      = "dependency"
	OperationApplication     = "application"
)

var (
	componentDotted       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	componentDistribution = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	componentNormalize    = regexp.MustCompile(`[-_.]+`)
)

// Key says which real operation this is, so two contracts can't bind it: an
// application declaration, or else the operation's full dotted name, however
// a manifest splits it between module and symbol, and whichever kind or
// distribution it names.
func (o ResolvedComponentOperation) Key() string {
	if o.Kind == OperationApplication {
		return o.Kind + "|" + o.Declaration
	}
	return "python|" + o.Module + "." + o.Symbol
}

// ValidComponentContractID says whether id can name a trusted contract:
// nonblank, with no spaces or control characters.
func ValidComponentContractID(id string) bool {
	return id != "" && strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

// ValidComponentOperation checks a resolved operation's shape alone.
func ValidComponentOperation(o ResolvedComponentOperation) bool {
	switch o.Kind {
	case OperationStandardLibrary:
		return o.Distribution == "" && o.Declaration == "" && componentDotted.MatchString(o.Module) && componentDotted.MatchString(o.Symbol)
	case OperationDependency:
		return o.Declaration == "" && componentDistribution.MatchString(o.Distribution) && normalizeDistribution(o.Distribution) == o.Distribution &&
			componentDotted.MatchString(o.Module) && componentDotted.MatchString(o.Symbol)
	case OperationApplication:
		return o.Distribution == "" && o.Module == "" && o.Symbol == "" && o.Declaration != ""
	}
	return false
}

// normalizeDistribution is PEP 503's normalized name, the form resolved
// wheels are recorded in.
func normalizeDistribution(name string) string {
	return componentNormalize.ReplaceAllString(strings.ToLower(name), "-")
}

func (r *componentResolver) trustedContracts(declarations []ComponentContract) ([]ResolvedComponentContract, error) {
	var resolved []ResolvedComponentContract
	ids, operations, contracts := map[string]bool{}, map[string]string{}, map[string]string{}
	for _, d := range declarations {
		if !ValidComponentContractID(d.ID) {
			return nil, fmt.Errorf("components: trusted contract id %q must be nonblank, without spaces or control characters", d.ID)
		}
		if ids[d.ID] {
			return nil, fmt.Errorf("components: duplicate trusted contract %q", d.ID)
		}
		ids[d.ID] = true
		fail := func(err error) error { return fmt.Errorf("components trusted contract %q: %w", d.ID, err) }
		op, operation, err := r.operation(d.Operation)
		if err != nil {
			return nil, fail(err)
		}
		if other, ok := operations[op.Key()]; ok {
			return nil, fail(fmt.Errorf("its operation already has trusted contract %q", other))
		}
		if other, ok := contracts[op.Declaration]; ok && op.Kind == OperationApplication {
			return nil, fail(fmt.Errorf("its operation %s is trusted contract %q's abstraction", op.Declaration, other))
		}
		operations[op.Key()] = d.ID
		contract, err := r.resolve(d.Contract, "contract declaration")
		if err != nil {
			return nil, fail(err)
		}
		_, inScope := r.scoped[d.Contract]
		switch {
		case r.testFile(contract.Source):
			return nil, fail(fmt.Errorf("its contract must be declared outside a test file"))
		case contract.ContractSHA256 == "":
			return nil, fail(fmt.Errorf("its contract %s has no supported inline Nagini contract", contract.ID))
		case inScope:
			return nil, fail(fmt.Errorf("its contract %s is production code in scope, not a trusted abstraction", contract.ID))
		case contract.ID == op.Declaration:
			return nil, fail(fmt.Errorf("its contract %s is the operation it describes", contract.ID))
		}
		if other, ok := contracts[contract.ID]; ok {
			return nil, fail(fmt.Errorf("its contract %s already holds trusted contract %q", contract.ID, other))
		}
		if other, ok := operations[(ResolvedComponentOperation{Kind: OperationApplication, Declaration: contract.ID}).Key()]; ok {
			return nil, fail(fmt.Errorf("its contract %s is trusted contract %q's operation", contract.ID, other))
		}
		contracts[contract.ID] = d.ID
		for _, c := range []Component{contract, operation} {
			if c.ID != "" {
				r.declarations[c.ID] = c
				r.provenanceFiles[c.Source] = ComponentFile{Path: c.Source, SHA256: c.SourceSHA256}
			}
		}
		if len(d.Tests) == 0 {
			return nil, fail(fmt.Errorf("it must select at least one backing test"))
		}
		out := ResolvedComponentContract{ID: d.ID, Operation: op, Contract: contract.ID}
		// What the contract lets its operation raise comes from the same
		// bytes its contract hash covers.
		source, err := r.read(contract.Source)
		if err != nil {
			return nil, fail(err)
		}
		for _, e := range source.exsures[contract.Symbol] {
			if e.Path == "" {
				return nil, fail(fmt.Errorf("its contract %s's Exsures(%s, ...) must name an exception class: a builtin, or a name its module imports at top level", contract.ID, e.Expression))
			}
			if !slices.Contains(out.Exsures, e.Path) {
				out.Exsures = append(out.Exsures, e.Path)
			}
		}
		if out.Tests, err = r.associations(d.Tests, "test"); err != nil {
			return nil, fail(err)
		}
		if out.Invocations, err = r.invocationsFor(d.Invocations, func(kind string) (string, []string, error) {
			if kind != "test" {
				return "", nil, fmt.Errorf("a trusted contract's invocations run its backing tests, so kind must be test, not %q", kind)
			}
			return "test", out.Tests, nil
		}); err != nil {
			return nil, fail(err)
		}
		resolved = append(resolved, out)
	}
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].ID < resolved[j].ID })
	return resolved, nil
}

// operation resolves what a trusted contract describes. An application
// operation's declaration may lie outside scope, and joins the provenance.
func (r *componentResolver) operation(o ComponentOperation) (ResolvedComponentOperation, Component, error) {
	switch o.Kind {
	case OperationStandardLibrary, OperationDependency:
		if o.Source != "" {
			return ResolvedComponentOperation{}, Component{}, fmt.Errorf("a %s operation names a module and symbol, not a source", o.Kind)
		}
		if !componentDotted.MatchString(o.Module) || !componentDotted.MatchString(o.Symbol) {
			return ResolvedComponentOperation{}, Component{}, fmt.Errorf("a %s operation needs a dotted Python module and symbol, not %q and %q", o.Kind, o.Module, o.Symbol)
		}
		out := ResolvedComponentOperation{Kind: o.Kind, Module: o.Module, Symbol: o.Symbol}
		if o.Kind == OperationStandardLibrary {
			if o.Distribution != "" {
				return ResolvedComponentOperation{}, Component{}, fmt.Errorf("a standard_library operation belongs to the Python runtime, not distribution %q", o.Distribution)
			}
			return out, Component{}, nil
		}
		if !componentDistribution.MatchString(o.Distribution) {
			return ResolvedComponentOperation{}, Component{}, fmt.Errorf("a dependency operation needs a distribution name, not %q", o.Distribution)
		}
		out.Distribution = normalizeDistribution(o.Distribution)
		return out, Component{}, nil
	case OperationApplication:
		if o.Distribution != "" || o.Module != "" {
			return ResolvedComponentOperation{}, Component{}, fmt.Errorf("an application operation names a source and symbol, not a module or distribution")
		}
		c, err := r.resolve(ComponentRef{Source: o.Source, Symbol: o.Symbol}, "operation")
		if err != nil {
			return ResolvedComponentOperation{}, Component{}, err
		}
		if r.testFile(c.Source) {
			return ResolvedComponentOperation{}, Component{}, fmt.Errorf("an application operation must be declared outside a test file")
		}
		return ResolvedComponentOperation{Kind: o.Kind, Declaration: c.ID}, c, nil
	}
	return ResolvedComponentOperation{}, Component{}, fmt.Errorf("unknown operation kind %q; want standard_library, dependency or application", o.Kind)
}
