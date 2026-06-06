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
// a vector<map<string, Variant>>. Pays no column-cell touch overhead
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
  RowFrame(Variant::object_t common, std::vector<Variant::object_t> items);

  // ---- Frame interface ----
  Variant common(const std::string& field) const override;
  bool has_common(const std::string& field) const override;
  void set_common(const std::string& field, Variant value) override;
  std::vector<std::string> common_fields() const override;

  std::size_t item_count() const override;
  Variant item(std::size_t index, const std::string& field) const override;
  bool item_has(std::size_t index, const std::string& field) const override;
  std::vector<std::string> item_fields() const override;

  void set_resources(const std::map<std::string, Variant>* res) override {
    resources_ = res;
  }
  const std::map<std::string, Variant>* resources() const override {
    return resources_;
  }

  void push_warning(std::string msg) override;
  std::vector<std::string> take_warnings() override;

  void apply_output(OperatorOutput& out, const std::string& op_name, bool is_recall) override;

  Result to_result(const std::vector<std::string>& common_out,
                   const std::vector<std::string>& item_out) const override;

  Variant::object_t item_object(std::size_t index) const override;

  std::unique_ptr<Frame> make_window_view(std::size_t row_offset, std::size_t row_count) const override;

  std::pair<std::string, int> validate_strict_items(const std::vector<std::string>& fields) const override;

  // Lock-free read-side mirrors (see Frame for contract).
  void with_read_lock(const std::function<void()>& body) const override;
  Variant common_no_lock(const std::string& field) const override;
  bool has_common_no_lock(const std::string& field) const override;
  std::size_t item_count_no_lock() const override;
  bool item_has_no_lock(std::size_t index, const std::string& field) const override;

 private:
  mutable std::shared_mutex mu_;
  Variant::object_t common_;
  std::vector<Variant::object_t> items_;
  std::vector<std::string> warnings_;
  const std::map<std::string, Variant>* resources_ = nullptr;

  // Window-view mode: when set, reads delegate to parent storage with
  // an (offset, count) translation; writes throw. Set only by
  // make_window_view.
  const Variant::object_t* view_common_ = nullptr;
  const std::vector<Variant::object_t>* view_items_ = nullptr;
  std::size_t view_offset_ = 0;
  std::size_t view_count_ = 0;
  bool is_window_view() const noexcept {
    return view_items_ != nullptr;
  }
};

}  // namespace pine
