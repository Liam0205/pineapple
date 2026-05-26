#pragma once

#include "pine/column.hpp"

#include <map>
#include <memory>
#include <set>
#include <string>
#include <vector>

namespace pine {

// ColumnStore is the abstract storage layer for a single row group's
// item columns. Per decision 04, this exists so an Arrow-backed
// implementation can be substituted without touching operator code.
//
// Each column shares the same row count (the row_count() invariant).
// Adding a new column with set_column() must use a column whose size
// matches row_count() (the caller is expected to back-fill).
class ColumnStore {
public:
    virtual ~ColumnStore() = default;

    virtual std::size_t row_count() const = 0;
    virtual std::vector<std::string> fields() const = 0;
    virtual bool has_column(const std::string& field) const = 0;
    virtual const Column* column(const std::string& field) const = 0;

    // Replace (or insert) `field`'s column. Column must have size ==
    // row_count() unless this is the first column being inserted into
    // an empty store, in which case its size sets row_count().
    virtual void set_column(const std::string& field, std::unique_ptr<Column> col) = 0;

    // Mutable access for in-place edits. The store guarantees a unique
    // mutable column reference is returned (auto-cloning if needed in
    // future COW variants). Returns nullptr if the field is absent.
    virtual Column* mutate_column(const std::string& field) = 0;

    // remove_rows / reorder_rows apply to every column atomically so
    // row_count() stays consistent. extend_rows(n, present=false)
    // appends n null cells to every existing column (used when items
    // are added).
    virtual void remove_rows(const std::set<int>& indices) = 0;
    virtual void reorder_rows(const std::vector<int>& order) = 0;
    virtual void extend_rows(std::size_t n) = 0;

    virtual std::unique_ptr<ColumnStore> clone() const = 0;
};

// TypedColumnStore is the default ColumnStore: an ordered map of
// std::unique_ptr<Column>. Future Arrow / shared-snapshot variants
// implement the same interface.
class TypedColumnStore final : public ColumnStore {
public:
    TypedColumnStore() = default;
    explicit TypedColumnStore(std::size_t row_count) : row_count_(row_count) {}

    std::size_t row_count() const override { return row_count_; }
    std::vector<std::string> fields() const override;
    bool has_column(const std::string& field) const override;
    const Column* column(const std::string& field) const override;

    void set_column(const std::string& field, std::unique_ptr<Column> col) override;
    Column* mutate_column(const std::string& field) override;

    void remove_rows(const std::set<int>& indices) override;
    void reorder_rows(const std::vector<int>& order) override;
    void extend_rows(std::size_t n) override;

    std::unique_ptr<ColumnStore> clone() const override;

private:
    std::map<std::string, std::unique_ptr<Column>> cols_;
    std::size_t row_count_ = 0;
};

}  // namespace pine
