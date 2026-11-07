package plumbing

import "github.com/gitdek/invariant/internal/repository"

// CheckDependencyInstall applies preparation's pure admission rules to supplied
// evidence. It neither reads the checkout nor prepares or runs an environment.
func CheckDependencyInstall(part repository.Part, files map[string][]byte) error {
	_, err := dependencyInstall(part, files)
	return err
}

// DecodeDependencyJSON uses the same duplicate-key and field-spelling checks
// as preparation when a caller reads additional manifest evidence.
func DecodeDependencyJSON(data []byte, value any) error {
	return dependencyJSON(data, value)
}

// CompatibleNodeVersion uses preparation's supported npm range grammar for
// runtime constraints too, without asking npm or evaluating repository code.
func CompatibleNodeVersion(version, constraint string) bool {
	compatible, err := npmRange(version, constraint)
	return err == nil && compatible
}
