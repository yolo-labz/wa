// Package buildstamp renders the build identity both binaries report.
//
// wa and wad are separate package mains, so before this each carried its
// own copy of the same four-case switch — and the two were free to drift
// into reporting the same build differently, which is the opposite of
// what a version banner is for. One function, one shape.
package buildstamp

import "fmt"

// Banner renders "version (commit @ date)", dropping whichever parts are
// unset. An unstamped build prints the bare version rather than empty
// parentheses or a stray "@".
func Banner(version, commit, date string) string {
	switch {
	case commit == "" && date == "":
		return version
	case commit != "" && date != "":
		return fmt.Sprintf("%s (%s @ %s)", version, commit, date)
	case commit != "":
		return fmt.Sprintf("%s (%s)", version, commit)
	default:
		return fmt.Sprintf("%s (@ %s)", version, date)
	}
}
