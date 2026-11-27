package capability

// HTTP adapters validate input and project responses only. Networking, auth,
// upstream status checks and billing stay in the shared HTTP path.
type httpSyncAdapter struct {
	validate func(map[string]any) *ExecutionError
	project  func(map[string]any, string) (map[string]any, bool, *ExecutionError)
}

var httpSyncAdapters = map[string]httpSyncAdapter{
	"tikhub-douyin-search": {validateDouyinInput, projectDouyinSearch},
}
