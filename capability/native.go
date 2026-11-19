package capability

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// NativeSyncSpec binds a synchronous capability to an in-process Go handler
// instead of an outbound HTTP endpoint. A Manifest opts in with
// "provider": "native" and "native": "<name>".
//
// Native capabilities keep the shared sync surface: Manifest validation,
// billing, consume logs, error envelopes and the registry (kill switch) are the
// same code paths as HTTP-backed capabilities. They must not call third-party
// paid APIs; see RULES.md "原生能力".
type NativeSyncSpec struct {
	// Validate runs after the generic Manifest checks and before billing.
	Validate func(m *Manifest, input map[string]any) *ExecutionError
	// Run executes the capability and returns its public output.
	Run func(ctx context.Context, m *Manifest, input map[string]any) (*SyncResult, *ExecutionError)
}

var (
	nativeSyncMu       sync.RWMutex
	nativeSyncHandlers = map[string]NativeSyncSpec{}
)

// RegisterNativeSync registers the Go handler referenced by a Manifest's
// "native" field. Registration normally happens from package init.
func RegisterNativeSync(name string, spec NativeSyncSpec) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("native handler name is required")
	}
	if !nativeNamePattern.MatchString(name) {
		return fmt.Errorf("native handler %q must be lowercase kebab-case", name)
	}
	if spec.Run == nil {
		return fmt.Errorf("native handler %s requires a Run function", name)
	}
	nativeSyncMu.Lock()
	defer nativeSyncMu.Unlock()
	nativeSyncHandlers[name] = spec
	return nil
}

func nativeSyncHandler(name string) (NativeSyncSpec, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return NativeSyncSpec{}, false
	}
	nativeSyncMu.RLock()
	defer nativeSyncMu.RUnlock()
	spec, ok := nativeSyncHandlers[name]
	return spec, ok
}

func validateNativeSyncInput(m *Manifest, input map[string]any) *ExecutionError {
	spec, ok := nativeSyncHandler(m.Native)
	if !ok {
		// Manifest validation rejects unregistered handlers; this is a
		// defensive path for runtime registrations.
		return &ExecutionError{
			Code:       "invalid_manifest",
			Message:    "native capability handler is not registered",
			HTTPStatus: 500,
		}
	}
	if spec.Validate == nil {
		return nil
	}
	return spec.Validate(m, input)
}
