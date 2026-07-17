package buildinfo

import "fmt"

var (
	Version = "0.2.0"
	Commit  = "unknown"
)

func String() string {
	if Commit == "" || Commit == "unknown" {
		return Version
	}
	return fmt.Sprintf("%s (%s)", Version, Commit)
}
