package lua

// activeBackendName reports which backend is currently linked. It is the
// single source of truth for backend-aware *test labels* — backend_isolation_test.go
// embeds it in t.Errorf / t.Fatalf messages so a failure tells you which VM
// produced it without sprinkling build tags through the test file. Backend-
// specific *invariants* (gopher's snapshotGlobals semantics vs wangshu's
// MarkGlobalsBaseline) still need their own //go:build-gated test files —
// pool_gopher_lua_test.go and pool_wangshu_test.go.
//
// Returns "gopher-lua" or "wangshu". The constant itself is defined under the
// build tag in pool_gopher_lua.go / backend_wangshu.go.
func activeBackendName() string { return backendName }
