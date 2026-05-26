#pragma once

#include "pine/frame.hpp"
#include "pine/pine.hpp"

#include <map>
#include <memory>
#include <shared_mutex>
#include <string>
#include <vector>

namespace pine {

// RowFrame is the row-major Frame implementation. Mirrors pine-go
// internal/dataframe/row_frame.go: common is a small map, items live as
// a vector<map<string, JsonValue>>. Pays no column-cell touch overhead
// on per-row access patterns (Lua snapshots, remote requests, observe
// logging, recall add_item). For column-wide batch scans ColumnFrame is
// still preferred.
//
// Internally thread-safe via shared_mutex (same locking discipline as
// ColumnFrame).
//
// Added when pine-cpp grew dual physical representation to match
// pine-go's Frame interface. Selected via Config.storage_mode = "row".
class RowFrame : public Frame {
public:
    RowFrame();
    RowFrame(std::map<std::string, JsonValue> common,
             std::vector<std::map<std::string, JsonValue>> items);

    // ---- Frame interface ----
    JsonValue common(const std::string& field) const override;
    bool has_common(const std::string& field) const override;
    void set_common(const std::string& field, JsonValue value) override;
    std::vector<std::string> common_fields() const override;

    std::size_t item_count() const override;
    JsonValue item(std::size_t index, const std::string& field) const override;
    bool item_has(std::size_t index, const std::string& field) const override;
    std::vector<std::string> item_fields() const override;

    void set_resources(const std::map<std::string, JsonValue>* res) override { resources_ = res; }
    const std::map<std::string, JsonValue>* resources() const override { return resources_; }

    void push_warning(std::string msg) override;
    std::vector<std::string> take_warnings() override;

    void apply_output(const OperatorOutput& out,
                      const std::string& op_name,
                      bool is_recall) override;

    Result to_result(const std::vector<std::string>& common_out,
                     const std::vector<std::string>& item_out) const override;

    JsonValue::object_t item_object(std::size_t index) const override;

    std::unique_ptr<Frame> make_window_view(std::size_t row_offset,
                                             std::size_t row_count) const override;

private:
    mutable std::shared_mutex mu_;
    std::map<std::string, JsonValue> common_;
    std::vector<std::map<std::string, JsonValue>> items_;
    std::vector<std::string> warnings_;
    const std::map<std::string, JsonValue>* resources_ = nullptr;

    // Window-view mode: when set, reads delegate to parent storage with
    // an (offset, count) translation; writes throw. Set only by
    // make_window_view.
    const std::map<std::string, JsonValue>* view_common_ = nullptr;
    const std::vector<std::map<std::string, JsonValue>>* view_items_ = nullptr;
    std::size_t view_offset_ = 0;
    std::size_t view_count_ = 0;
    bool is_window_view() const noexcept { return view_items_ != nullptr; }
};

}  // namespace pine
