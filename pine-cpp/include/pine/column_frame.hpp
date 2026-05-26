#pragma once

#include "pine/column_store.hpp"
#include "pine/pine.hpp"

#include <map>
#include <memory>
#include <shared_mutex>
#include <string>
#include <vector>

namespace pine {

// ColumnFrame is the request-local DataFrame backed by a ColumnStore.
// Mirrors pine-go internal/dataframe/column_frame.go in semantics:
//   - common is a small row map
//   - items live in a column store with per-field validity bitmap
//   - apply_output runs the canonical five-stage write log
//     (common writes, item writes, removals, reorder, additions)
//   - to_result projects strict common/item field lists into Result
//
// Internally thread-safe via shared_mutex (shared for reads, unique for
// writes). The engine's per-operator scheduling lock can be relaxed to
// rely on this.
class ColumnFrame {
public:
    ColumnFrame();
    ColumnFrame(std::map<std::string, JsonValue> common,
                std::vector<std::map<std::string, JsonValue>> items);

    // Construct a non-owning read-only window over a parent frame's items.
    // common_ and items_ are shared by pointer; (row_offset, row_count)
    // selects a contiguous slice of the parent's rows.
    //
    // All mutating methods (set_common / apply_output / push_warning)
    // throw PanicError on a window view. Used by parallel_execute (P2-05)
    // to avoid row-major reification when sharding — operators see the
    // shard's row range but never copy column data.
    //
    // CONTRACT: the caller must keep `parent` alive AND must not mutate
    // `parent` while any window view exists. parallel_execute satisfies
    // both: shards execute synchronously between cv-waits on the parent
    // node, and the parent frame is read-only during the shard window.
    static std::unique_ptr<ColumnFrame> make_window_view(
        const ColumnFrame& parent,
        std::size_t row_offset,
        std::size_t row_count);

    // ---- common ----
    JsonValue common(const std::string& field) const;
    bool has_common(const std::string& field) const;
    void set_common(const std::string& field, JsonValue value);
    std::vector<std::string> common_fields() const;

    // ---- items ----
    std::size_t item_count() const;
    JsonValue item(std::size_t index, const std::string& field) const;
    bool item_has(std::size_t index, const std::string& field) const;
    std::vector<std::string> item_fields() const;

    // ---- resources (read-only injected map) ----
    void set_resources(const std::map<std::string, JsonValue>* res) { resources_ = res; }
    const std::map<std::string, JsonValue>* resources() const { return resources_; }

    // ---- warnings ----
    void push_warning(std::string msg);
    std::vector<std::string> take_warnings();
    const std::vector<std::string>& warnings_ref() const { return warnings_; }

    // ---- apply OperatorOutput (write log) ----
    // Runs the canonical five-stage application:
    //   1. common writes
    //   2. item writes (auto-creates columns)
    //   3. removals
    //   4. reorder
    //   5. additions  (recall ops stamp _source = op_name on each added row)
    //   6. warning    (first-wins via push_warning)
    void apply_output(const OperatorOutput& out,
                      const std::string& op_name,
                      bool is_recall);

    // Project the frame to a Result using the strict common/item field
    // lists (skips fields whose validity is false on a given row).
    Result to_result(const std::vector<std::string>& common_out,
                     const std::vector<std::string>& item_out) const;

    // ---- snapshots / read-only views ----
    // Returns the entire item[index] as a JsonObject for fields that
    // are present. Used by snapshot_input / debug paths.
    JsonValue::object_t item_object(std::size_t index) const;

private:
    void write_item_field_locked(std::size_t idx,
                                 const std::string& field,
                                 const JsonValue& value);

    mutable std::shared_mutex mu_;
    std::map<std::string, JsonValue> common_;
    std::unique_ptr<ColumnStore> items_;
    std::vector<std::string> warnings_;
    const std::map<std::string, JsonValue>* resources_ = nullptr;

    // Window-view mode (P2-05). When non-null, all reads delegate to
    // the parent's storage with a (offset, count) translation, and all
    // writes throw PanicError. Set only by make_window_view().
    const ColumnStore* view_items_ = nullptr;
    const std::map<std::string, JsonValue>* view_common_ = nullptr;
    std::size_t view_offset_ = 0;
    std::size_t view_count_ = 0;
    bool is_window_view() const noexcept { return view_items_ != nullptr; }
};

}  // namespace pine
