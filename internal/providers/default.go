package providers

import (
	"sync"

	"ccgateway/internal/provider"
	"ccgateway/internal/provider/claudevendor"
	"ccgateway/internal/provider/codex"
)

var (
	once sync.Once
	reg  *provider.Registry
)

func DefaultRegistry() *provider.Registry {
	once.Do(func() {
		r := provider.NewRegistry()
		r.MustRegister(claudevendor.NewBundle())
		r.MustRegister(codex.NewBundle())
		reg = r
	})
	return reg
}
