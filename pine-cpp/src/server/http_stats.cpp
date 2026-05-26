#include "http_stats.hpp"

namespace pine {
namespace server {

void HttpStats::record_request(const std::string& method, const std::string& path,
                                const std::string& status_bucket, int64_t duration_ns) {
    const std::string req_key = method + " " + path + " " + status_bucket;
    const std::string dur_key = method + " " + path;
    std::lock_guard<std::mutex> lock(mu_);
    ++requests_[req_key];
    auto& bucket = durations_[dur_key];
    ++bucket.count;
    bucket.sum_ns += duration_ns;
}

std::map<std::string, int64_t> HttpStats::requests_snapshot() const {
    std::lock_guard<std::mutex> lock(mu_);
    return requests_;
}

std::map<std::string, HttpStats::DurationBucket> HttpStats::durations_snapshot() const {
    std::lock_guard<std::mutex> lock(mu_);
    return durations_;
}

}  // namespace server
}  // namespace pine
