#pragma once

#include <algorithm>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace pine {

// FlatMap: a sorted vector<pair<string, V>> providing unordered_map-compatible
// interface. For small N (typical JSON objects have 5-15 keys), binary search
// on contiguous memory is faster than hash table pointer chasing.
//
// Invariant: entries_ is always sorted by key. Duplicate keys are not allowed.
template <typename V>
class FlatMap {
 public:
  using key_type = std::string;
  using mapped_type = V;
  using value_type = std::pair<std::string, V>;
  using iterator = typename std::vector<value_type>::iterator;
  using const_iterator = typename std::vector<value_type>::const_iterator;

  FlatMap() = default;

  template <typename InputIt>
  FlatMap(InputIt first, InputIt last) : entries_(first, last) {
    std::sort(entries_.begin(), entries_.end(),
              [](const value_type& a, const value_type& b) { return a.first < b.first; });
  }

  FlatMap(std::initializer_list<value_type> init) : entries_(init) {
    std::sort(entries_.begin(), entries_.end(),
              [](const value_type& a, const value_type& b) { return a.first < b.first; });
  }

  iterator begin() noexcept { return entries_.begin(); }
  iterator end() noexcept { return entries_.end(); }
  const_iterator begin() const noexcept { return entries_.begin(); }
  const_iterator end() const noexcept { return entries_.end(); }
  const_iterator cbegin() const noexcept { return entries_.cbegin(); }
  const_iterator cend() const noexcept { return entries_.cend(); }

  bool empty() const noexcept { return entries_.empty(); }
  std::size_t size() const noexcept { return entries_.size(); }

  void clear() noexcept { entries_.clear(); }

  void reserve(std::size_t n) { entries_.reserve(n); }

  iterator find(const std::string& key) {
    auto it = lower_bound(key);
    if (it != entries_.end() && it->first == key) {
      return it;
    }
    return entries_.end();
  }

  const_iterator find(const std::string& key) const {
    auto it = lower_bound(key);
    if (it != entries_.end() && it->first == key) {
      return it;
    }
    return entries_.end();
  }

  V& operator[](const std::string& key) {
    auto it = lower_bound(key);
    if (it != entries_.end() && it->first == key) {
      return it->second;
    }
    it = entries_.emplace(it, key, V{});
    return it->second;
  }

  V& operator[](std::string&& key) {
    auto it = lower_bound(key);
    if (it != entries_.end() && it->first == key) {
      return it->second;
    }
    it = entries_.emplace(it, std::move(key), V{});
    return it->second;
  }

  template <typename... Args>
  std::pair<iterator, bool> emplace(const std::string& key, Args&&... args) {
    auto it = lower_bound(key);
    if (it != entries_.end() && it->first == key) {
      return {it, false};
    }
    it = entries_.emplace(it, key, V(std::forward<Args>(args)...));
    return {it, true};
  }

  template <typename... Args>
  std::pair<iterator, bool> emplace(std::string&& key, Args&&... args) {
    auto it = lower_bound(key);
    if (it != entries_.end() && it->first == key) {
      return {it, false};
    }
    it = entries_.emplace(it, std::move(key), V(std::forward<Args>(args)...));
    return {it, true};
  }

  std::size_t count(const std::string& key) const {
    return find(key) != end() ? 1 : 0;
  }

  const V& at(const std::string& key) const {
    auto it = find(key);
    if (it == end()) {
      throw std::out_of_range("FlatMap::at: key not found");
    }
    return it->second;
  }

  V& at(const std::string& key) {
    auto it = find(key);
    if (it == end()) {
      throw std::out_of_range("FlatMap::at: key not found");
    }
    return it->second;
  }

  iterator erase(iterator pos) {
    return entries_.erase(pos);
  }

  std::size_t erase(const std::string& key) {
    auto it = find(key);
    if (it == end()) {
      return 0;
    }
    entries_.erase(it);
    return 1;
  }

  // Insert a pre-sorted range without re-sorting. Caller must guarantee
  // the range is sorted and non-overlapping with existing entries.
  // Used by the JSON parser which builds objects in sorted order.
  void insert_presorted(std::vector<value_type>&& sorted_entries) {
    entries_ = std::move(sorted_entries);
  }

 private:
  iterator lower_bound(const std::string& key) {
    return std::lower_bound(entries_.begin(), entries_.end(), key,
                            [](const value_type& entry, const std::string& k) {
                              return entry.first < k;
                            });
  }

  const_iterator lower_bound(const std::string& key) const {
    return std::lower_bound(entries_.begin(), entries_.end(), key,
                            [](const value_type& entry, const std::string& k) {
                              return entry.first < k;
                            });
  }

  std::vector<value_type> entries_;
};

}  // namespace pine
