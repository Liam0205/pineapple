// Regression test for #103 / #109 / #131: parent and window view must
// share the same shared_mutex, otherwise concurrent
// `parent.apply_output` (write under parent.mu_) and shard
// `build_operator_input` (read under view.mu_) hold two distinct locks
// guarding the same underlying storage, producing data races on the
// FlatMap that #131's nightly TSan replay caught in production.
//
// The repro spins two threads in tight loops:
//   - Writer: parent.apply_output with N item_writes per call
//   - Reader: build_operator_input on a window view, walking the same
//     FlatMap entries via item_has_no_lock → FlatMap::find
// and lets them run for 2 seconds. Pre-fix this triggers ThreadSanitizer
// reports within milliseconds; post-fix it runs cleanly.
//
// The doctest assertions only check that the loop completes without
// throwing — the actual race detection comes from running this binary
// under -fsanitize=thread (cpp-tsan CI job).
#include "pine/column_frame.hpp"
#include "pine/frame.hpp"
#include "pine/pine.hpp"
#include "pine/row_frame.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <chrono>
#include <memory>
#include <string>
#include <thread>
#include <vector>

using namespace pine;

namespace {

constexpr int kRowCount = 200;
constexpr int kFieldCount = 8;
constexpr auto kRunFor = std::chrono::milliseconds(500);

template <typename FrameT>
std::unique_ptr<FrameT> make_seed_frame() {
  Variant::object_t common{{"region", Variant(std::string("us"))}};
  std::vector<Variant::object_t> items;
  items.reserve(kRowCount);
  for (int i = 0; i < kRowCount; ++i) {
    Variant::object_t row;
    row["id"] = Variant(static_cast<double>(i));
    for (int f = 0; f < kFieldCount; ++f) {
      row["field_" + std::to_string(f)] = Variant(static_cast<double>(i + f));
    }
    items.push_back(std::move(row));
  }
  return std::make_unique<FrameT>(std::move(common), std::move(items));
}

// Drives the writer-vs-reader race scenario for `kRunFor`. Returns the
// (writer, reader) iteration counts so the test can assert both sides
// actually got CPU time.
struct RaceCounts {
  long writer_iters = 0;
  long reader_iters = 0;
};

template <typename FrameT>
RaceCounts run_window_view_race(FrameT& parent) {
  // Build the view BEFORE starting the writer. data_parallel does this
  // synchronously per request, but we want both threads to start with
  // the view already aliased to parent.items_.
  auto view = parent.make_window_view(0, kRowCount / 2);

  std::atomic<bool> stop{false};
  std::atomic<long> writer_iters{0};
  std::atomic<long> reader_iters{0};

  // Writer: apply_output on parent. Each iteration does N item_writes,
  // matching the workload that #131's TSan stack caught:
  // RowFrame::apply_output → FlatMap::operator[] (vector emplace).
  std::thread writer([&] {
    while (!stop.load(std::memory_order_relaxed)) {
      OperatorOutput out;
      const long n = writer_iters.load(std::memory_order_relaxed);
      for (int i = 0; i < kRowCount; ++i) {
        for (int f = 0; f < kFieldCount; ++f) {
          out.set_item(i, "writer_" + std::to_string(f), Variant(static_cast<double>(n)));
        }
      }
      try {
        parent.apply_output(out, "writer_op", /*is_recall=*/false);
      } catch (...) {
        // benign; continue racing
      }
      writer_iters.fetch_add(1, std::memory_order_relaxed);
    }
  });

  // Reader: walk the view's items via has-field probes. This corresponds
  // to build_operator_input → with_read_lock → item_has_no_lock →
  // FlatMap::find → lower_bound (string compare), which is exactly
  // T21's stack in #131's TSan report.
  std::thread reader([&] {
    while (!stop.load(std::memory_order_relaxed)) {
      for (std::size_t i = 0; i < view->item_count(); ++i) {
        for (int f = 0; f < kFieldCount; ++f) {
          (void)view->item_has(i, "field_" + std::to_string(f));
          (void)view->item_has(i, "writer_" + std::to_string(f));
        }
      }
      reader_iters.fetch_add(1, std::memory_order_relaxed);
    }
  });

  std::this_thread::sleep_for(kRunFor);
  stop.store(true, std::memory_order_relaxed);
  writer.join();
  reader.join();

  return {writer_iters.load(std::memory_order_relaxed), reader_iters.load(std::memory_order_relaxed)};
}

}  // namespace

TEST_CASE("RowFrame: window view shares parent mutex (#103 / #109 / #131)") {
  auto parent = make_seed_frame<RowFrame>();
  auto counts = run_window_view_race(*parent);
  // Both threads should have made progress — if either is 0, the test
  // didn't actually exercise the race surface.
  CHECK(counts.writer_iters > 0);
  CHECK(counts.reader_iters > 0);
  // Under -fsanitize=thread this TEST_CASE should produce zero TSan
  // reports post-fix. Pre-fix (parent.mu_ ≠ view.mu_) it triggers
  // WARNINGs within the first few iterations on the FlatMap entries
  // mutated by apply_output.
}

TEST_CASE("ColumnFrame: window view shares parent mutex (#103 / #109 / #131)") {
  auto parent = make_seed_frame<ColumnFrame>();
  auto counts = run_window_view_race(*parent);
  CHECK(counts.writer_iters > 0);
  CHECK(counts.reader_iters > 0);
}
