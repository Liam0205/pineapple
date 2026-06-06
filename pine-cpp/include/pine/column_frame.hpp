#pragma once

#include "pine/column_store.hpp"
#include "pine/frame.hpp"
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
//
// Implements the Frame interface — Engine selects ColumnFrame or
// RowFrame based on Config.storage_mode.
class ColumnFrame : public Frame {
 public:
  ColumnFrame();
  ColumnFrame(Variant::object_t common, std::vector<Variant::object_t> items);

  // Static convenience constructor kept for callers that already know
  // they want a ColumnFrame window view. parallel_execute uses the
  // virtual Frame::make_window_view on the parent — this static form
  // returns a ColumnFrame-typed unique_ptr for tests / legacy code.
  //
  // CONTRACT: the caller must keep `parent` alive AND must not mutate
  // `parent` while any window view exists. parallel_execute satisfies
  // both: shards execute synchronously between cv-waits on the parent
  // node, and the parent frame is read-only during the shard window.
  static std::unique_ptr<ColumnFrame> make_window_view(const ColumnFrame& parent, std::size_t row_offset,
                                                       std::size_t row_count);

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
  const std::vector<std::string>& warnings_ref() const {
    return warnings_;
  }

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
  Variant item_no_lock(std::size_t index, const std::string& field) const override;

 private:
  void write_item_field_locked(std::size_t idx, const std::string& field, const Variant& value);

  mutable std::shared_mutex mu_;
  Variant::object_t common_;
  std::unique_ptr<ColumnStore> items_;
  std::vector<std::string> warnings_;
  const std::map<std::string, Variant>* resources_ = nullptr;

  // Window-view mode. When non-null, all reads delegate to
  // the parent's storage with a (offset, count) translation, and all
  // writes throw PanicError. Set only by make_window_view().
  const ColumnStore* view_items_ = nullptr;
  const Variant::object_t* view_common_ = nullptr;
  std::size_t view_offset_ = 0;
  std::size_t view_count_ = 0;
  bool is_window_view() const noexcept {
    return view_items_ != nullptr;
  }
};

}  // namespace pine
