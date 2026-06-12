package lua

// activeBackendName reports which backend is currently linked. Single source of
// truth for backend-aware test skips so we don't sprinkle build tags through
// *_test.go files.
//
// Returns "gopher-lua" or "wangshu". The constant itself is defined under the
// build tag in pool_gopher_lua.go / backend_wangshu.go.
func activeBackendName() string { return backendName }
