package backends

import (
	"sync"

	"ccgateway/internal/backend"
	"ccgateway/internal/backend/builtin"
	"ccgateway/internal/backend/cliproxyapi"
)

var (
	once sync.Once
	reg  *backend.Registry
)

func DefaultRegistry() *backend.Registry {
	once.Do(func() {
		r := backend.NewRegistry()
		r.MustRegister(cliproxyapi.NewBundle())
		r.MustRegister(builtin.NewBundle())
		reg = r
	})
	return reg
}
