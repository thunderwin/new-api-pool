package capability

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

//go:embed manifests/*.json
var manifestFS embed.FS

var (
	registryMu sync.RWMutex
	registry   = map[string]*Manifest{}
	loadOnce   sync.Once
)

func loadEmbedded() {
	paths, err := fs.Glob(manifestFS, "manifests/*.json")
	if err != nil {
		common.SysLog("capability: failed to list manifests: " + err.Error())
		return
	}
	for _, path := range paths {
		raw, err := fs.ReadFile(manifestFS, path)
		if err != nil {
			common.SysLog("capability: failed to read manifest " + path)
			continue
		}
		var m Manifest
		if err := common.Unmarshal(raw, &m); err != nil {
			common.SysLog("capability: invalid manifest " + path)
			continue
		}
		if err := m.Validate(); err != nil {
			common.SysLog("capability: " + err.Error())
			continue
		}
		registry[m.ID] = &m
	}
}

func Get(id string) (*Manifest, bool) {
	loadOnce.Do(loadEmbedded)
	registryMu.RLock()
	defer registryMu.RUnlock()
	m, ok := registry[id]
	return m, ok
}

func List() []*Manifest {
	loadOnce.Do(loadEmbedded)
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*Manifest, 0, len(registry))
	for _, m := range registry {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Register allows tests and a future admin UI to add a manifest at runtime.
// Embedded manifests are loaded on first access.
func Register(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	if err := m.Validate(); err != nil {
		return err
	}
	loadOnce.Do(loadEmbedded)
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[m.ID] = m
	return nil
}

func Unregister(id string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registry, id)
}
