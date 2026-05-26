#pragma once

#include "redis/redis_client.hpp"

#include <chrono>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <tuple>
#include <vector>

namespace pine {
namespace redis {

// ConnectionPool caches Redis connections keyed by (host, port, db, password)
// so that hot operators (transform_redis_get / transform_redis_set) avoid
// the full getaddrinfo + socket + connect + AUTH + SELECT round-trip on
// every dispatch. P1-P4.
//
// Acquire returns a connection (creating a new one if the idle queue for
// the key is empty); release returns it to the queue. Both calls are
// thread-safe. The destructor closes every idle connection.
//
// Idle entries carry a steady_clock timestamp. acquire discards any
// idle handle older than kIdleTimeout before handing it out (so the
// caller never sees a connection the broker has likely closed already).
// release caps each key's idle queue at kMaxIdlePerKey — surplus
// connections are destroyed rather than pooled, bounding memory when a
// spike subsides. Health-pinging on reuse (PING before each acquire) is
// still a follow-up; the timestamp + connected() check covers most
// silent-broker-close cases. (P2-28)
class ConnectionPool {
public:
    static constexpr std::size_t kMaxIdlePerKey = 16;
    static constexpr std::chrono::seconds kIdleTimeout{60};

    ConnectionPool() = default;
    ~ConnectionPool() = default;

    ConnectionPool(const ConnectionPool&) = delete;
    ConnectionPool& operator=(const ConnectionPool&) = delete;

    // Borrow a connection. May construct a new client if none is idle.
    std::unique_ptr<Client> acquire(const std::string& host, int port,
                                    const std::string& password, int db);

    // Return a connection to the idle queue. The caller must not reuse
    // the pointer afterward. If the connection is in a broken state
    // (`!c->connected()`) it is destroyed instead of returned.
    void release(const std::string& host, int port,
                 const std::string& password, int db,
                 std::unique_ptr<Client> c);

private:
    using Key = std::tuple<std::string, int, std::string, int>;
    struct IdleEntry {
        std::unique_ptr<Client> client;
        std::chrono::steady_clock::time_point queued_at;
    };
    std::mutex mu_;
    std::map<Key, std::vector<IdleEntry>> idle_;
};

// Process-wide pool. Lives as long as the binary; suitable for both the
// HTTP server and the run CLI.
ConnectionPool& shared_pool();

}  // namespace redis
}  // namespace pine
