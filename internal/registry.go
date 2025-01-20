package internal

import (
	"fmt"
	"github.com/Project-Faster/quicly-go/quiclylib/types"
	"sync"
)

var connectionsRegistry map[uint64]types.Session
var mtx_registry sync.Mutex
var TracingOn = false

func ResetRegistry() {
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	connectionsRegistry = make(map[uint64]types.Session)
}

func RegisterConnection(s types.Session, id uint64) {
	if s == nil {
		return
	}
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	for _, st := range connectionsRegistry {
		if s.ID() == st.ID() {
			LogTraceMessage("skipped add connection id: %d found: %d (%v)\n", id, st.ID(), &s)
			return
		}
	}

	if connectionsRegistry[id] == nil || connectionsRegistry[id].ID() != s.ID() {
		LogTraceMessage("added connection id: %d index: %d (%v)\n", s.ID(), id, &s)
		connectionsRegistry[id] = s
	}
}

func GetConnection(id uint64) (types.Session, bool) {
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	s, ok := connectionsRegistry[id]
	return s, ok
}

func RemoveConnection(id uint64) {
	mtx_registry.Lock()
	defer mtx_registry.Unlock()

	if s, ok := connectionsRegistry[id]; ok {
		LogTraceMessage("removed connection id: %d index: %d (%v)\n", s.ID(), id, &s)
		delete(connectionsRegistry, id)
	}
}

func LogTraceMessage(format string, args ...interface{}) {
	if TracingOn {
		fmt.Printf(format, args...)
	}
}
