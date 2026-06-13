//go:build !lua_gopher

// Package lua's wangshu-backend implementation — the DEFAULT Lua VM. wangshu
// (https://github.com/Liam0205/wangshu) is a pure-Go Lua 5.1 VM with NaN-boxing
// and arena GC; its v0.1.4 CallInto zero-alloc boundary path (issue #8) makes it
// faster and lower-allocation than gopher-lua on per-item transform_by_lua
// workloads. Build with `-tags=lua_gopher` to fall back to gopher-lua.
//
// wangshu公共面填齐了 transform_by_lua 算子需要的全部能力:
//   - SetGlobal/GetGlobal/State.Call (issue #1, v0.1.1)
//   - Public Table API for common-mode list/map globals (issue #2, v0.1.2)
//   - HideFileLoaders strict sandbox matching gopher-lua semantics (issue #3, v0.1.2)
//   - SetContext/RemoveContext for honoring Go ctx cancellation (issue #4, v0.1.2)
//   - Table.ForEach to read script-returned map tables (issue #5, v0.1.3)
//   - MarkGlobalsBaseline/ResetGlobalsToBaseline for pool reuse isolation (issue #6, v0.1.3)
//   - CallInto zero-alloc boundary path (issue #8, v0.1.4)
package lua

import (
	"github.com/Liam0205/wangshu"
)

func init() {
	// Wire the package-level Backend variable to the wangshu factory (default).
	// gopher-lua's counterpart lives behind the lua_gopher build tag, so exactly
	// one init runs in any given binary.
	backend = wangshuBackend{}
}

// backendName is the build-tag-selected identifier surfaced to tests via
// activeBackendName(). Mirror in pool_gopher_lua.go.
const backendName = "wangshu"

type wangshuBackend struct{}

func (wangshuBackend) NewPool(script string) (Pool, error) {
	return newWangshuPool(script)
}

// wangshuOptions builds the State Options used for every pool state.
// HideFileLoaders=true keeps script-level sandbox semantics in lockstep with
// the gopher-lua backend: loadfile / dofile / loadstring / load resolve to nil
// in globals, so calling them raises `attempt to call a nil value` instead of
// returning the PUC 5.1.5 (nil, errmsg) tuple. AllowFileLoad stays false (any
// runtime file read is out of scope for pineapple's sandbox model anyway).
func wangshuOptions() wangshu.Options {
	return wangshu.Options{HideFileLoaders: true}
}
