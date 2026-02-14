package version

// Version, GitCommit, and BuildDate are set at build time via ldflags:
//
//	go build -ldflags "-X github.com/newtron-network/newtron/pkg/version.Version=v1.0.0 \
//	  -X github.com/newtron-network/newtron/pkg/version.GitCommit=abc1234 \
//	  -X github.com/newtron-network/newtron/pkg/version.BuildDate=2026-01-01T00:00:00Z"
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// Info returns a formatted version string for display.
func Info() string {
	return Version + " (" + GitCommit + ") built " + BuildDate
}
