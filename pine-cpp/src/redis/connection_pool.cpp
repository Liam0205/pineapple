#include "redis/connection_pool.hpp"

namespace pine {
namespace redis {

std::unique_ptr<Client> ConnectionPool::acquire(const std::string& host, int port,
                                                const std::string& password, int db) {
  Key key{host, port, password, db};
  const auto now = std::chrono::steady_clock::now();
  {
    std::lock_guard<std::mutex> lk(mu_);
    auto it = idle_.find(key);
    if (it != idle_.end()) {
      // Pop newest-first (LIFO) and drop anything that has been idle
      // longer than kIdleTimeout. Stale handles are very likely
      // closed by the broker side already; surfacing them as errors
      // on first use defeats the purpose of pooling.
      while (!it->second.empty()) {
        auto entry = std::move(it->second.back());
        it->second.pop_back();
        if (now - entry.queued_at <= kIdleTimeout && entry.client->connected()) {
          return std::move(entry.client);
        }
        // entry.client destructs here, closing the stale socket.
      }
    }
  }
  return std::make_unique<Client>(host, port, password, db);
}

void ConnectionPool::release(const std::string& host, int port, const std::string& password, int db,
                             std::unique_ptr<Client> c) {
  if (!c || !c->connected()) {
    return;
  }
  Key key{host, port, password, db};
  std::lock_guard<std::mutex> lk(mu_);
  auto& bucket = idle_[key];
  // Bound the idle queue: a workload spike can push many handles into
  // the pool; without the cap they sit there indefinitely, eating fd
  // budget for a key that may never see traffic again.
  if (bucket.size() >= kMaxIdlePerKey) {
    return;  // c destructs here
  }
  bucket.push_back({std::move(c), std::chrono::steady_clock::now()});
}

ConnectionPool& shared_pool() {
  static ConnectionPool pool;
  return pool;
}

}  // namespace redis
}  // namespace pine
