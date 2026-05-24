#pragma once

#include "redis/redis_client.hpp"

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
// thread-safe. The destructor closes every idle connection. There is no
// idle-timeout or health-check yet — a connection that the broker tore
// down silently will surface as an exception on the next read/write and
// the caller is expected to retry rather than reuse the same handle.
class ConnectionPool {
public:
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

    // ScopedClient is the RAII handle returned by acquire_scoped. It owns
    // the Client and bundles the pool + key so the destructor can release
    // back without the caller wiring up a per-operator guard. Moved-from
    // instances are inert. (P2-29)
    class ScopedClient {
    public:
        ScopedClient() = default;
        ScopedClient(ConnectionPool* pool,
                     std::string host, int port,
                     std::string password, int db,
                     std::unique_ptr<Client> client)
            : pool_(pool), host_(std::move(host)), port_(port),
              password_(std::move(password)), db_(db),
              client_(std::move(client)) {}

        ScopedClient(const ScopedClient&) = delete;
        ScopedClient& operator=(const ScopedClient&) = delete;

        ScopedClient(ScopedClient&& o) noexcept
            : pool_(o.pool_), host_(std::move(o.host_)), port_(o.port_),
              password_(std::move(o.password_)), db_(o.db_),
              client_(std::move(o.client_)) { o.pool_ = nullptr; }

        ScopedClient& operator=(ScopedClient&& o) noexcept {
            if (this == &o) return *this;
            release_now();
            pool_ = o.pool_; o.pool_ = nullptr;
            host_ = std::move(o.host_); port_ = o.port_;
            password_ = std::move(o.password_); db_ = o.db_;
            client_ = std::move(o.client_);
            return *this;
        }

        ~ScopedClient() { release_now(); }

        Client* get() const noexcept { return client_.get(); }
        Client* operator->() const noexcept { return client_.get(); }
        explicit operator bool() const noexcept { return static_cast<bool>(client_); }

    private:
        void release_now() {
            if (pool_ && client_) pool_->release(host_, port_, password_, db_, std::move(client_));
        }

        ConnectionPool* pool_ = nullptr;
        std::string host_;
        int port_ = 0;
        std::string password_;
        int db_ = 0;
        std::unique_ptr<Client> client_;
    };

    // Convenience: acquire + bundle into a ScopedClient that releases on
    // scope exit. Returns an empty ScopedClient if the underlying acquire
    // returned null (connection failure).
    ScopedClient acquire_scoped(const std::string& host, int port,
                                const std::string& password, int db) {
        auto c = acquire(host, port, password, db);
        if (!c) return ScopedClient{};
        return ScopedClient{this, host, port, password, db, std::move(c)};
    }

private:
    using Key = std::tuple<std::string, int, std::string, int>;
    std::mutex mu_;
    std::map<Key, std::vector<std::unique_ptr<Client>>> idle_;
};

// Process-wide pool. Lives as long as the binary; suitable for both the
// HTTP server and the run CLI.
ConnectionPool& shared_pool();

}  // namespace redis
}  // namespace pine
