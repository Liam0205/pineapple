#pragma once

#include <chrono>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <tuple>
#include <vector>

#include "redis/redis_client.hpp"

namespace pine {
namespace redis {

// ConnectionPool caches Redis connections keyed by (host, port, db, password)
// so that hot operators (transform_redis_get / transform_redis_set) avoid
// the full getaddrinfo + socket + connect + AUTH + SELECT round-trip on
// every dispatch.
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
// silent-broker-close cases.
class ConnectionPool {
 public:
  static constexpr std::size_t kMaxIdlePerKey = 16;
  static constexpr std::chrono::seconds kIdleTimeout{60};

  ConnectionPool() = default;
  ~ConnectionPool() = default;

  ConnectionPool(const ConnectionPool&) = delete;
  ConnectionPool& operator=(const ConnectionPool&) = delete;

  // Borrow a connection. May construct a new client if none is idle.
  std::unique_ptr<Client> acquire(const std::string& host, int port, const std::string& password, int db);

  // Return a connection to the idle queue. The caller must not reuse
  // the pointer afterward. If the connection is in a broken state
  // (`!c->connected()`) it is destroyed instead of returned.
  void release(const std::string& host, int port, const std::string& password, int db,
               std::unique_ptr<Client> c);

  // ScopedClient is the RAII handle returned by acquire_scoped. It owns
  // the Client and bundles the pool + key so the destructor can release
  // back without the caller wiring up a per-operator guard. Moved-from
  // instances are inert.
  class ScopedClient {
   public:
    ScopedClient() = default;
    ScopedClient(ConnectionPool* pool, std::string host, int port, std::string password, int db,
                 std::unique_ptr<Client> client)
        : pool_(pool),
          host_(std::move(host)),
          port_(port),
          password_(std::move(password)),
          db_(db),
          client_(std::move(client)) {
    }

    ScopedClient(const ScopedClient&) = delete;
    ScopedClient& operator=(const ScopedClient&) = delete;

    ScopedClient(ScopedClient&& o) noexcept
        : pool_(o.pool_),
          host_(std::move(o.host_)),
          port_(o.port_),
          password_(std::move(o.password_)),
          db_(o.db_),
          client_(std::move(o.client_)) {
      o.pool_ = nullptr;
    }

    ScopedClient& operator=(ScopedClient&& o) noexcept {
      if (this == &o) {
        return *this;
      }
      release_now();
      pool_ = o.pool_;
      o.pool_ = nullptr;
      host_ = std::move(o.host_);
      port_ = o.port_;
      password_ = std::move(o.password_);
      db_ = o.db_;
      client_ = std::move(o.client_);
      return *this;
    }

    ~ScopedClient() {
      release_now();
    }

    Client* get() const noexcept {
      return client_.get();
    }
    Client* operator->() const noexcept {
      return client_.get();
    }
    explicit operator bool() const noexcept {
      return static_cast<bool>(client_);
    }

   private:
    void release_now() {
      if (pool_ && client_) {
        pool_->release(host_, port_, password_, db_, std::move(client_));
      }
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
  ScopedClient acquire_scoped(const std::string& host, int port, const std::string& password, int db) {
    auto c = acquire(host, port, password, db);
    if (!c) {
      return ScopedClient{};
    }
    return ScopedClient{this, host, port, password, db, std::move(c)};
  }

 private:
  using Key = std::tuple<std::string, int, std::string, int>;
  struct IdleEntry {
    std::unique_ptr<Client> client;
    std::chrono::steady_clock::time_point queued_at;
  };
  std::mutex mu_;
  std::map<Key, std::vector<IdleEntry>> idle_;
};

// RedisConnResource is the handle stored by the `redis_connection` resource
// (operators/transform/redis_connection.cpp) and borrowed by Redis operators
// via resource_name. It owns a ConnectionPool scoped to a single
// (host,port,password,db); acquire() hands out a pooled client. Multiple
// operators referencing the same resource_name share one RedisConnResource
// (one pool). The pool — and all its idle connections — is torn down when the
// ResourceManager retires and the last borrower releases its shared_ptr,
// mirroring pine-go's *redis.Client being closed on resource retirement.
class RedisConnResource {
 public:
  RedisConnResource(std::string host, int port, std::string password, int db)
      : host_(std::move(host)), port_(port), password_(std::move(password)), db_(db) {
  }

  RedisConnResource(const RedisConnResource&) = delete;
  RedisConnResource& operator=(const RedisConnResource&) = delete;

  ConnectionPool::ScopedClient acquire() {
    return pool_.acquire_scoped(host_, port_, password_, db_);
  }

  const std::string& host() const {
    return host_;
  }

 private:
  std::string host_;
  int port_ = 0;
  std::string password_;
  int db_ = 0;
  ConnectionPool pool_;
};

}  // namespace redis
}  // namespace pine
