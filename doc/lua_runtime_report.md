# Lua Runtime Report

## Overview

The Lua runtime enables user-defined Lua scripts to be executed as operators inside the Pine engine. It is built on [gopher-lua](https://github.com/yuin/gopher-lua), a pure-Go Lua 5.1 VM. Two execution modes are supported:

- **function_for_item**: Calls the named Lua function once per item. Common fields are set as scalar globals; item fields are set as scalar globals per iteration.
- **function_for_common**: Calls the named Lua function once for all items. Common fields are scalar globals; item fields are Lua tables (1-indexed arrays).

## Architecture

| Component | File | Purpose |
|-----------|------|---------|
| Operator | `operators/lua/lua.go` | `LuaOp` — Init, Execute, goToLua/luaToGo conversion |
| State Pool | `operators/lua/pool.go` | `statePool` — `sync.Pool`-based Lua state management with _G snapshot/restore |

### State Pool Design

- `Init`: loads the script into the first Lua state, snapshots `_G` as a baseline.
- `Borrow`: returns a state from `sync.Pool` (or creates one lazily).
- `Return`: clears non-baseline globals, returns the state to the pool.

This ensures each `Execute` call gets a clean, isolated global environment without paying the cost of script re-compilation.

### MetadataAware Integration

`LuaOp` implements the `MetadataAware` interface to receive declared field names from `$metadata`:
- `commonInput` / `commonOutput` — fields read/written as scalar globals
- `itemInput` / `itemOutput` — fields read/written as scalars (item mode) or tables (common mode)
- Return values map positionally to output field names.

## Correctness Validation

### Unit Tests (operators/lua/)

| Test | Coverage |
|------|----------|
| `TestLuaOpInitBothFuncs` | Error when both functions set |
| `TestLuaOpInitNoFunc` | Error when no function set |
| `TestLuaOpInitBadScript` | Error for invalid Lua syntax |
| `TestLuaOpForItem` | Per-item discount (child user) |
| `TestLuaOpForItemAdult` | Per-item passthrough (adult user) |
| `TestLuaOpForCommon` | Aggregation: avg and max |
| `TestLuaOpForCommonBool` | Boolean control flow evaluation |
| `TestLuaOpForItemEmpty` | Empty items — no writes |
| `TestLuaOpFunctionNotFound` | Missing function error |
| `TestLuaOpNilHandling` | Nil input → LNil → handled in script |
| `TestLuaOpMultipleReturns` | Multiple return values mapped to item_output |
| `TestLuaOpConcurrent` | 20-goroutine concurrent execution |
| `TestLuaOpStringReturn` | String concatenation return |
| `TestNewStatePool` | Pool creation and basic function call |
| `TestNewStatePoolBadScript` | Pool rejects bad script |
| `TestSnapshotAndClear` | _G cleanup removes non-baseline globals |
| `TestPoolConcurrent` | 10-goroutine concurrent borrow/return |

**Coverage: 89.1%** (statements)

### Integration Tests (integration/)

| Test | Scenario |
|------|----------|
| `TestLuaPipelineE2E` | recall → Lua discount (child) → Lua stats → Lua control flow → sort |
| `TestLuaPipelineAdult` | Same pipeline, adult user (no discount) |
| `TestLuaPipelineConcurrent` | 20 concurrent requests with varying ages |

### Concurrency & Safety

- `go test -race ./...`: **PASS** — no data races detected.
- `go vet ./...`: **PASS** — no issues.

## Performance

Benchmarks run on Apple M5, 3 iterations each.

### Lua Per-Item (function_for_item)

| Items | Latency (ns/op) | Allocs/op | Bytes/op |
|-------|-----------------|-----------|----------|
| 100 | ~74,500 | ~1,350 | ~195 KB |
| 1000 | ~858,000 | ~12,650 | ~1.96 MB |

### Lua Per-Common (function_for_common)

| Items | Latency (ns/op) | Allocs/op | Bytes/op |
|-------|-----------------|-----------|----------|
| 100 | ~54,300 | ~1,057 | ~165 KB |
| 1000 | ~563,900 | ~9,810 | ~1.62 MB |

### Lua Control Flow (common check + conditional sort)

| Items | Latency (ns/op) | Allocs/op | Bytes/op |
|-------|-----------------|-----------|----------|
| 100 | ~75,500 | ~1,190 | ~219 KB |

### Assessment

- **Per-item mode** scales linearly with item count (~750 ns/item at 1000 items). The overhead includes Lua global set/get and function invocation per item.
- **Per-common mode** is faster per-invocation (single Lua call). The overhead is dominated by Lua table construction for item field arrays.
- **Control flow** (evaluate condition, skip downstream) adds negligible overhead beyond the underlying Lua call + sort.
- **State pool** effectively amortizes VM creation cost — no script recompilation per request.

## Files Added/Modified

| File | Change |
|------|--------|
| `operators/lua/lua.go` | New — Lua operator implementation |
| `operators/lua/pool.go` | New — Lua state pool |
| `operators/lua/lua_test.go` | New — 13 unit tests |
| `operators/lua/pool_test.go` | New — 4 pool tests |
| `operators/all.go` | Modified — added lua blank import |
| `internal/types/operator.go` | Modified — added MetadataAware interface |
| `internal/types/operator_io.go` | Modified — added CommonKeys/ItemKeys helpers |
| `operator.go` | Modified — re-exported MetadataAware |
| `pine.go` | Modified — passes metadata to MetadataAware operators |
| `testdata/e2e_lua_pipeline.json` | New — Lua integration config |
| `integration/lua_e2e_test.go` | New — 3 integration tests |
| `benchmarks/bench_test.go` | Modified — added 6 Lua benchmarks |
