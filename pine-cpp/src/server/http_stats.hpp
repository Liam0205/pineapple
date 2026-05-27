#pragma once

#include <cstdint>
#include <map>
#include <mutex>
#include <string>

namespace pine {
namespace server {

// Per-process atomic accumulators for HTTP request observability. Mirrors
// pine-go pkg/server.HttpStats: the HTTP metrics middleware writes both an
// external Provider (Counter/Histogram) and this in-memory structure so
// /stats can expose request counts and duration sums without requiring a
// Prometheus adapter.
//
// Keys are byte-exact with the Go reference:
//   requests_total:           "<METHOD> <path> <statusBucket>"
//   request_duration_seconds: "<METHOD> <path>"
class HttpStats {
 public:
  struct DurationBucket {
    int64_t count = 0;
    int64_t sum_ns = 0;
  };

  void record_request(const std::string& method, const std::string& path, const std::string& status_bucket,
                      int64_t duration_ns);

  // Returns ordered (lexicographic) maps for deterministic JSON output.
  // Mirrors pine-go's sort.Strings + map serialization.
  std::map<std::string, int64_t> requests_snapshot() const;
  std::map<std::string, DurationBucket> durations_snapshot() const;

 private:
  mutable std::mutex mu_;
  std::map<std::string, int64_t> requests_;
  std::map<std::string, DurationBucket> durations_;
};

}  // namespace server
}  // namespace pine
