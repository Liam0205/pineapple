---
name: dag-implicit-row-set-fix-v080
description: v0.8.0 DAG implicit row-set dependency fix and ConsumesRowSet marker cleanup, auto-inject mechanism, dag-differential-fuzz infrastructure
type: reflection
---

# [DAG Implicit Row-Set Dependency Fix -- v0.8.0]

## Task

v0.7 three-marker model (ConsumesRowSet/MutatesRowSet/AdditiveWritesRowSet) missed a class of operators: those with item_input or item_output fields but no explicit ConsumesRowSet marker. Without the marker, these operators had no `_row_set_` dependency edge, meaning they could run concurrently with or before Filter/Reorder, causing `SetItem index out of range` when item indices shift underneath them. The fix introduced auto-inject of `_row_set_` read for any operator with non-empty item fields, cleaned up 6 operators that no longer need explicit ConsumesRowSet, added DAG-level differential fuzz testing, and bumped to v0.8.0.

## Expected vs Actual

- **Expected**: The v0.7 three-marker model fully captures all row-set dependency scenarios. ConsumesRowSet is an opt-in marker for operators that structurally depend on the row set.
- **Actual**: Item-field operators (transform_copy, transform_dispatch, transform_normalize, transform_resource_lookup, transform_by_lua, observe_log) operated on items via item_input/item_output, making them structurally dependent on a stable row set, but the three-marker model treated ConsumesRowSet as purely optional. If an operator author forgot to add the marker, no safety net existed. The bug was latent because all built-in operators happened to have ConsumesRowSet manually applied, but any new operator or external operator would silently lack the dependency edge.

## What Went Wrong

1. **Design gap in the three-marker model**: The v0.7 model described ConsumesRowSet as "optional for Transform/Observe" without recognizing that item-field access inherently implies row-set consumption. The model was conceptually clean (three explicit markers) but practically incomplete (a fourth mechanism was needed to close the gap).

2. **Marker redundancy masked the latent bug**: All 6 affected built-in operators already had ConsumesRowSet applied manually. The bug was invisible in existing test suites because every concrete operator was individually correct. Only an operator *without* the marker would exhibit the failure, making the design gap easy to miss during the v0.7 redesign.

3. **No fuzz-level invariant for row-set safety**: The existing DAG fuzz (`FuzzBuild`) checked graph structural properties (pred/succ symmetry, topological legality) but did not assert the semantic invariant "every operator with item fields must have a `_row_set_` dependency edge". The gap was discoverable by property-based testing but no such property existed.

4. **No cross-engine DAG-level differential fuzz**: `scripts/differential-fuzz.py` tested execution-level parity (same inputs, same outputs), but DAG construction parity (same edges, same dependency ordering) was not verified across engines. An engine could build a structurally different graph that happened to produce correct output for specific test inputs but would diverge under different scheduling.

## Root Cause

The v0.7 three-marker model was designed around explicit opt-in semantics: operators declare their row-set relationship. But item-field access (item_input/item_output in metadata) already carries an implicit structural signal that the operator touches items and therefore depends on a stable row set. The model failed to recognize this implicit signal as a sufficient condition for `_row_set_` read injection, leaving a gap between "metadata declares item fields" and "DAG enforces row-set ordering".

The root cause is a category mismatch: the marker model treated row-set dependency as a behavioral declaration (operator knows it needs the row set), when it is actually also a structural property (operator touches items, therefore it needs the row set). The fix bridges this by auto-injecting `_row_set_` read based on the structural signal (non-empty item_input OR item_output), making the behavioral declaration (ConsumesRowSet) only necessary for operators with no item fields but which still structurally depend on the row set (e.g., transform_size calls ItemCount(), transform_remote_pineapple reads items unconditionally).

## Missing Docs or Signals

### 1. dag-engine.md: "three-marker model" is now incomplete

The architecture doc describes the v0.7 model as three markers plus `_row_set_` sentinel. After this fix, there are effectively four mechanisms:

- `AdditiveWritesRowSet` -- additive write to `_row_set_`
- `MutatesRowSet` -- destructive write to `_row_set_`
- `ConsumesRowSet` -- explicit read of `_row_set_` (now only needed for implicit structural deps)
- **Auto-inject** -- implicit read of `_row_set_` for any operator with item_input or item_output

The doc should describe auto-inject as the primary safety net, with ConsumesRowSet as the fallback for edge cases.

### 2. dag-engine.md: Type table says ConsumesRowSet is "optional" for Transform/Observe

The type table row for Transform reads `| Transform | ... | optional ConsumesRowSet |`. This is now misleading. After auto-inject, most Transform/Observe operators get their `_row_set_` dependency automatically. ConsumesRowSet is only needed when:

- The operator has no item fields in metadata but still accesses the row set (transform_size, transform_remote_pineapple)
- The operator's item-field metadata is empty but it calls ItemCount() or iterates items unconditionally

### 3. operator-contract.md: "optional ConsumesRowSet" guidance

The operator contract says ConsumesRowSet is optional for Transform/Observe. Post-fix, the guidance should explain: if your operator declares item_input or item_output, the engine auto-injects the dependency; you only need explicit ConsumesRowSet if you access items without declaring item fields.

### 4. ci-quality-baseline.md: missing dag-differential-fuzz.py

The new `scripts/dag-differential-fuzz.py` (DAG-level three-engine differential fuzz) is not mentioned. It generates random pipeline configs, builds DAGs in all three engines, and compares edge sets and topological orderings. This is distinct from `scripts/differential-fuzz.py` (execution-level parity).

### 5. Fuzz invariants not documented

The row-set safety invariant added to Go/Java/Python fuzz tests ("every operator with item fields must have a `_row_set_` dependency edge") is a new semantic property assertion in the fuzz layer. The fuzz section in ci-quality-baseline.md does not mention semantic invariants beyond "not panic".

## Promotion Candidates

### Should promote to `architecture/dag-engine.md`

- Auto-inject mechanism as the fourth row-set dependency mechanism, described alongside the three markers. The "three-marker model" label should be updated or clarified to include auto-inject.
- Revised rules for when explicit ConsumesRowSet is actually needed (no item fields in metadata but structurally reads the row set).
- Updated type table: Transform/Observe ConsumesRowSet column should distinguish "auto-injected via item fields" from "explicit marker needed".

### Should promote to `reference/operator-contract.md`

- Updated guidance on ConsumesRowSet: "If your operator declares item_input or item_output in metadata, the engine auto-injects `_row_set_` read dependency. Explicit ConsumesRowSet is only needed if you access items (e.g., ItemCount()) without declaring item fields."
- The two concrete examples where ConsumesRowSet is still needed: transform_size (calls ItemCount() with no item fields) and transform_remote_pineapple (unconditionally reads items and sends downstream).

### Should promote to `guides/ci-quality-baseline.md`

- `scripts/dag-differential-fuzz.py` as a new test infrastructure entry: DAG-level three-engine differential fuzz, distinct from execution-level differential fuzz.
- Row-set safety invariant in Go/Java/Python fuzz tests as an example of semantic property assertions beyond "not panic".

### Stay in memory only

- The specific list of 6 operators cleaned up (transform_copy, transform_dispatch, transform_normalize, transform_resource_lookup, transform_by_lua, observe_log) -- this is historical cleanup detail.
- The specific reasoning for why transform_size and transform_remote_pineapple retain ConsumesRowSet -- useful for memory but too granular for stable docs.
- The 15-commit progression (bug doc, test, fix across 3 engines, cleanup across 3 engines, fixtures, fuzz infra, version bump) -- workflow detail.

## Follow-up

1. Update `architecture/dag-engine.md`: Add auto-inject as the fourth mechanism in the row-set dependency model. Revise the type table and the "v0.7 three-marker model" description.
2. Update `reference/operator-contract.md`: Clarify when explicit ConsumesRowSet is needed vs auto-injected.
3. Update `guides/ci-quality-baseline.md`: Add dag-differential-fuzz.py entry and row-set safety invariant description.
4. Consider renaming "three-marker model" to "marker + auto-inject model" or "four-mechanism model" in documentation to prevent the old label from causing confusion.
