package version

// Version, GitCommit, and BuildTime are set at build time via ldflags:
//
//	go build -ldflags "-X github.com/aldrin-isaac/newtron/pkg/version.Version=v1.0.0 \
//	  -X github.com/aldrin-isaac/newtron/pkg/version.GitCommit=abc1234 \
//	  -X github.com/aldrin-isaac/newtron/pkg/version.BuildTime=2026-06-19T18:00:00Z"
//
// BuildTime must be RFC3339-formatted UTC; the schema metadata endpoints
// (api/handler_schema.go) parse it for the Last-Modified header. When
// BuildTime is empty (development builds, broken inject), the schema
// handler falls back to process start time — slightly less cache-
// friendly across rolling restarts but functionally correct.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = ""
)
