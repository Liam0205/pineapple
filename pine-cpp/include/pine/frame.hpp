#pragma once

#include "pine/pine.hpp"

#include <map>
#include <memory>
#include <string>
#include <vector>

namespace pine {

// Frame is the request-local DataFrame abstraction. Two physical
// implementations satisfy this interface:
//   - ColumnFrame (default): items live in a typed ColumnStore with per-field
//     validity bitmap. Cache-friendly for batch column scans.
//   - RowFrame: items live as a vector<map<string, JsonValue>>. Cheaper for
//     per-row access patterns (Lua snapshots, remote requests, observe
//     logging) and avoids the column-cell touch overhead when the request
//     is short or sparse.
//
// Both implementations are thread-safe internally. Engine selects based on
// Config.storage_mode ("column" / "row"); storage_mode falls back to
// "column" when unrecognised.
//
// R3-L3: Frame was previously `using Frame = ColumnFrame;` (single-impl).
// Promoted to virtual base when pine-cpp grew RowFrame to match
// pine-go's dual physical representation (decision-04 / decision-14
// "MVP single impl" relaxed).
class Frame {
public:
    virtual ~Frame() = default;

    // ---- common ----
    virtual JsonValue common(const std::string& field) const = 0;
    virtual bool has_common(const std::string& field) const = 0;
    virtual void set_common(const std::string& field, JsonValue value) = 0;
    virtual std::vector<std::string> common_fields() const = 0;

    // ---- items ----
    virtual std::size_t item_count() const = 0;
    virtual JsonValue item(std::size_t index, const std::string& field) const = 0;
    virtual bool item_has(std::size_t index, const std::string& field) const = 0;
    virtual std::vector<std::string> item_fields() const = 0;

    // ---- resources (read-only injected map) ----
    virtual void set_resources(const std::map<std::string, JsonValue>* res) = 0;
    virtual const std::map<std::string, JsonValue>* resources() const = 0;

    // ---- warnings ----
    virtual void push_warning(std::string msg) = 0;
    virtual std::vector<std::string> take_warnings() = 0;

    // ---- apply OperatorOutput (write log) ----
    virtual void apply_output(const OperatorOutput& out,
                              const std::string& op_name,
                              bool is_recall) = 0;

    // Project the frame to a Result using the strict common/item field
    // lists (skips fields whose validity is false on a given row).
    virtual Result to_result(const std::vector<std::string>& common_out,
                             const std::vector<std::string>& item_out) const = 0;

    // ---- snapshots / read-only views ----
    virtual JsonValue::object_t item_object(std::size_t index) const = 0;

    // Non-owning read-only window over a parent frame's items. Both
    // implementations support this for parallel_execute (P2-05). Reads
    // delegate to the parent with an (offset, count) translation; writes
    // throw.
    virtual std::unique_ptr<Frame> make_window_view(std::size_t row_offset,
                                                     std::size_t row_count) const = 0;

    // Batch-validate strict item fields. Returns ("", -1) if all rows pass.
    // On failure returns (field_name, row_index) of the first violation.
    // ColumnFrame uses bitmap scans; RowFrame checks per-row maps.
    virtual std::pair<std::string, int> validate_strict_items(
        const std::vector<std::string>& fields) const = 0;
};

// Factory: build the Frame implementation that matches storage_mode.
// Unknown / empty storage_mode falls back to "column". (R3-L3)
std::unique_ptr<Frame> make_frame(const std::string& storage_mode,
                                   std::map<std::string, JsonValue> common,
                                   std::vector<std::map<std::string, JsonValue>> items);

}  // namespace pine
