package agent

import (
	"sync"

	model "github.com/great-magician-01/callable/internal/model"
)

// toolSet is an ordered, name-keyed tool registry used by the Agent. It is
// safe for concurrent use: sub-agent loading registers tools at runtime while
// other sessions of the same agent may be listing tools.
type toolSet struct {
	mu     sync.RWMutex
	order  []model.Tool
	byName map[string]model.Tool
}

func newToolSet() *toolSet {
	return &toolSet{byName: map[string]model.Tool{}}
}

// add registers tools; a tool whose name is already taken is skipped, so
// user-defined tools win over built-ins.
func (s *toolSet) add(tools ...model.Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range tools {
		if t == nil {
			continue
		}
		name := t.Definition().Name
		if _, exists := s.byName[name]; exists {
			continue
		}
		s.byName[name] = t
		s.order = append(s.order, t)
	}
}

func (s *toolSet) list() []model.Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]model.Tool{}, s.order...)
}

func (s *toolSet) get(name string) (model.Tool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.byName[name]
	return t, ok
}

func (s *toolSet) len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.order)
}
