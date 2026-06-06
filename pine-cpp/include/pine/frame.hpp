#pragma once

#include "pine/pine.hpp"

#include <functional>
#include <map>
#include <memory>
#include <string>
#include <unordered_map>
#include <vector>

namespace pine {

// Frame is the request-local DataFrame abstraction. Two physical
// implementations satisfy this interface:
//   - ColumnFrame (default): items live in a typed ColumnStore with per-field
//     validity bitmap. Cache-friendly for batch column scans.
//   - RowFrame: items live as a vector<map<string, Variant>>. Cheaper for
//     per-row access patterns (Lua snapshots, remote requests, observe
//     logging) and avoids the column-cell touch overhead when the request
//     is short or sparse.
//
// Both implementations are thread-safe internally. Engine selects based on
// Config.storage_mode ("column" / "row"); storage_mode falls back to
// "column" when unrecognised.
//
// Frame was previously `using Frame = ColumnFrame;` (single-impl).
// Promoted to virtual base when pine-cpp grew RowFrame to match
// pine-go's dual physical representation (decision-04 / decision-14
// "MVP single impl" relaxed).
class Frame {
 public:
  virtual ~Frame() = default;

  // ---- common ----
  virtual Variant common(const std::string& field) const = 0;
  virtual bool has_common(const std::string& field) const = 0;
  virtual void set_common(const std::string& field, Variant value) = 0;
  virtual std::vector<std::string> common_fields() const = 0;

  // ---- items ----
  virtual std::size_t item_count() const = 0;
  virtual Variant item(std::size_t index, const std::string& field) const = 0;
  virtual bool item_has(std::size_t index, const std::string& field) const = 0;
  virtual std::vector<std::string> item_fields() const = 0;

  // ---- resources (read-only injected map) ----
  virtual void set_resources(const std::map<std::string, Variant>* res) = 0;
  virtual const std::map<std::string, Variant>* resources() const = 0;

  // ---- warnings ----
  virtual void push_warning(std::string msg) = 0;
  virtual std::vector<std::string> take_warnings() = 0;

  // ---- apply OperatorOutput (write log) ----
  // Non-const reference: apply_output may consume `out` (e.g. RowFrame
  // moves added_items into items_ to skip a per-row copy). All callers
  // hold `out` as a non-const local and discard it after the call —
  // post-apply mutation is safe.
  virtual void apply_output(OperatorOutput& out, const std::string& op_name, bool is_recall) = 0;

  // Project the frame to a Result using the strict common/item field
  // lists (skips fields whose validity is false on a given row).
  virtual Result to_result(const std::vector<std::string>& common_out,
                           const std::vector<std::string>& item_out) const = 0;

  // ---- snapshots / read-only views ----
  virtual Variant::object_t item_object(std::size_t index) const = 0;

  // Non-owning read-only window over a parent frame's items. Both
  // implementations support this for parallel_execute. Reads
  // delegate to the parent with an (offset, count) translation; writes
  // throw.
  virtual std::unique_ptr<Frame> make_window_view(std::size_t row_offset, std::size_t row_count) const = 0;

  // Batch-validate strict item fields. Returns ("", -1) if all rows pass.
  // On failure returns (field_name, row_index) of the first violation.
  // ColumnFrame uses bitmap scans; RowFrame checks per-row maps.
  virtual std::pair<std::string, int> validate_strict_items(const std::vector<std::string>& fields) const = 0;

  // ---- internal: lock-free read-side mirrors ----
  //
  // The public read methods (common/has_common/item/item_has/item_count)
  // each acquire the internal shared_mutex per call. Hot paths that read
  // many fields under one logical "no concurrent writer" window (e.g.
  // build_operator_input's strict/nullable validation loop, which fires
  // 5000 × M times per request on large fixtures) collapse those calls
  // by taking the read lock once via with_read_lock and then dispatching
  // through the *_no_lock variants below — mirroring pine-go RowFrame
  // BuildInput (`f.mu.RLock(); defer f.mu.RUnlock(); ...`) and pine-java
  // DataFrame.buildInput (`rwLock.readLock().lock(); try { ... }`).
  //
  // Contract: the *_no_lock methods are only safe to call from a callable
  // invoked through with_read_lock() on the same Frame instance.
  virtual void with_read_lock(const std::function<void()>& body) const = 0;
  virtual Variant common_no_lock(const std::string& field) const = 0;
  virtual bool has_common_no_lock(const std::string& field) const = 0;
  virtual std::size_t item_count_no_lock() const = 0;
  virtual bool item_has_no_lock(std::size_t index, const std::string& field) const = 0;
  virtual Variant item_no_lock(std::size_t index, const std::string& field) const = 0;
};

// Factory: build the Frame implementation that matches storage_mode.
// Unknown / empty storage_mode falls back to "column".
std::unique_ptr<Frame> make_frame(const std::string& storage_mode, Variant::object_t common,
                                  std::vector<Variant::object_t> items);

}  // namespace pine
