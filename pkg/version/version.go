package version

// Version and GitCommit are set at build time via ldflags:
//
//	go build -ldflags "-X github.com/newtron-network/newtron/pkg/version.Version=v1.0.0 \
//	  -X github.com/newtron-network/newtron/pkg/version.GitCommit=abc1234"
var (
	Version   = "dev"
	GitCommit = "unknown"
)
