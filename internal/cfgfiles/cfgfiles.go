// Package cfgfiles manages the Xymon-format configuration files gomon keeps
// in its database (analysis.cfg, client-local.cfg) and their parsed forms.
package cfgfiles

import (
	"context"
	"errors"
	"sync"

	"gomon/internal/analysis"
	"gomon/internal/store"
)

const (
	AnalysisCfg    = "analysis.cfg"
	ClientLocalCfg = "client-local.cfg"
)

var Names = []string{AnalysisCfg, ClientLocalCfg}

var defaults = map[string]string{
	AnalysisCfg:    analysis.DefaultAnalysisCfg,
	ClientLocalCfg: analysis.DefaultClientLocalCfg,
}

type Manager struct {
	store *store.Store

	mu          sync.RWMutex
	raw         map[string]string
	rules       *analysis.Rules
	clientLocal *analysis.ClientLocal
}

func New(st *store.Store) *Manager {
	return &Manager{store: st, raw: map[string]string{}}
}

func (m *Manager) Load(ctx context.Context) error {
	for _, n := range Names {
		content, _, err := m.store.GetConfigFile(ctx, n)
		if errors.Is(err, store.ErrNotFound) {
			content = defaults[n]
		} else if err != nil {
			return err
		}
		if err := m.apply(n, content); err != nil {
			// keep going with what parsed; the UI shows the error on save
			_ = err
		}
	}
	return nil
}

func (m *Manager) apply(name, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.raw[name] = content
	switch name {
	case AnalysisCfg:
		r, err := analysis.Parse(content)
		m.rules = r
		return err
	case ClientLocalCfg:
		m.clientLocal = analysis.ParseClientLocal(content)
	}
	return nil
}

// Validate parses content without storing it.
func Validate(name, content string) error {
	if name == AnalysisCfg {
		_, err := analysis.Parse(content)
		return err
	}
	if _, ok := defaults[name]; !ok {
		return errors.New("unknown config file")
	}
	return nil
}

func (m *Manager) Put(ctx context.Context, name, content string) error {
	if err := Validate(name, content); err != nil {
		return err
	}
	if err := m.store.PutConfigFile(ctx, name, content); err != nil {
		return err
	}
	return m.apply(name, content)
}

func (m *Manager) Get(name string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.raw[name]
	return s, ok
}

func (m *Manager) Rules() *analysis.Rules {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rules
}

func (m *Manager) ClientLocal() *analysis.ClientLocal {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clientLocal
}
