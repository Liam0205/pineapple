#pragma once

#include "pine/metrics.hpp"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <thread>
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
  // Newly-constructed clients honour `opts` (read/write/dial timeouts).
  std::unique_ptr<Client> acquire(const std::string& host, int port, const std::string& password, int db,
                                  const ClientOptions& opts = ClientOptions{});

  // Return a connection to the idle queue. The caller must not reuse
  // the pointer afterward. If the connection is in a broken state
  // (`!c->connected()`) it is destroyed instead of returned.
  void release(const std::string& host, int port, const std::string& password, int db,
               std::unique_ptr<Client> c);

  // Number of connections currently checked out (acquired but not yet
  // released). Used by RedisConnResource to compute the total-conns gauge.
  std::size_t in_use_count() const {
    return in_use_.load(std::memory_order_relaxed);
  }

  // Number of connections sitting idle across all keys. Used for the
  // idle-conns gauge.
  std::size_t idle_count() const;

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
  ScopedClient acquire_scoped(const std::string& host, int port, const std::string& password, int db,
                              const ClientOptions& opts = ClientOptions{}) {
    auto c = acquire(host, port, password, db, opts);
    if (!c) {
      return ScopedClient{};
    }
    return ScopedClient{this, host, port, password, db, std::move(c)};
  }

  // Override the per-key idle-queue cap from the default kMaxIdlePerKey.
  // 0 (the default) keeps the legacy cap. Used by RedisConnResource to
  // surface the redis_connection resource's pool_size knob.
  void set_max_idle_per_key(std::size_t cap) {
    max_idle_per_key_ = cap == 0 ? kMaxIdlePerKey : cap;
  }

 private:
  using Key = std::tuple<std::string, int, std::string, int>;
  struct IdleEntry {
    std::unique_ptr<Client> client;
    std::chrono::steady_clock::time_point queued_at;
  };
  mutable std::mutex mu_;
  std::map<Key, std::vector<IdleEntry>> idle_;
  std::atomic<std::size_t> in_use_{0};
  std::size_t max_idle_per_key_ = kMaxIdlePerKey;
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
  // Probe cadence: how often the background thread samples pool stats and
  // pings the server. Fixed across runtimes so metric cadence is comparable
  // (matches pine-go's redisProbeInterval / pine-java's PROBE_INTERVAL_SECONDS).
  static constexpr std::chrono::seconds kProbeInterval{15};

  // Constructs the resource. When metrics_name is empty (or mp is null) no
  // metrics are created and no probe thread is started, mirroring pine-go's
  // newRedisConnResource gate. `opts` carries the cascade-safety timeouts
  // exposed by the redis_connection resource schema; `pool_size` (when > 0)
  // overrides the per-key idle-queue cap.
  RedisConnResource(std::string host, int port, std::string password, int db,
                    const std::string& metrics_name = "", metrics::Provider* mp = nullptr,
                    const ClientOptions& opts = ClientOptions{}, std::size_t pool_size = 0)
      : host_(std::move(host)), port_(port), password_(std::move(password)), db_(db), opts_(opts) {
    pool_.set_max_idle_per_key(pool_size);
    if (metrics_name.empty() || mp == nullptr) {
      return;
    }
    const std::vector<std::string> labels{metrics_name};
    total_conns_ = mp->new_gauge({"pine_redis_pool_total_conns",
                                  "Total Redis connections in the pool (idle + in-use).",
                                  {"name"}})
                       ->with(labels);
    idle_conns_ =
        mp->new_gauge({"pine_redis_pool_idle_conns", "Idle Redis connections in the pool.", {"name"}})
            ->with(labels);
    metrics::HistogramOpts hopts;
    hopts.opts = {"pine_redis_ping_duration_seconds", "Redis PING probe latency in seconds.", {"name"}};
    ping_duration_ = mp->new_histogram(hopts)->with(labels);
    up_ = mp->new_gauge(
                {"pine_redis_up", "Whether the last Redis PING probe succeeded (1) or failed (0).", {"name"}})
              ->with(labels);
    probe_thread_ = std::thread([this] { probe_loop(); });
  }

  RedisConnResource(const RedisConnResource&) = delete;
  RedisConnResource& operator=(const RedisConnResource&) = delete;

  // Stops the probe thread (if any). The pool — and all its idle connections —
  // is destroyed afterward.
  ~RedisConnResource() {
    {
      std::lock_guard<std::mutex> lk(stop_mu_);
      stopping_ = true;
    }
    stop_cv_.notify_all();
    if (probe_thread_.joinable()) {
      probe_thread_.join();
    }
  }

  ConnectionPool::ScopedClient acquire() {
    return pool_.acquire_scoped(host_, port_, password_, db_, opts_);
  }

  const std::string& host() const {
    return host_;
  }

 private:
  // probe_loop samples pool stats and PING latency every kProbeInterval until
  // the destructor signals stop. It runs one probe immediately so the metrics
  // are populated before the first tick.
  void probe_loop() {
    auto probe = [this] {
      total_conns_->set(static_cast<double>(pool_.in_use_count() + pool_.idle_count()));
      idle_conns_->set(static_cast<double>(pool_.idle_count()));
      const auto start = std::chrono::steady_clock::now();
      bool ok = false;
      try {
        auto c = pool_.acquire_scoped(host_, port_, password_, db_, opts_);
        ok = c && c->ping();
      } catch (...) {
        ok = false;
      }
      ping_duration_->observe(metrics::duration_seconds(std::chrono::steady_clock::now() - start));
      up_->set(ok ? 1.0 : 0.0);
    };
    probe();
    std::unique_lock<std::mutex> lk(stop_mu_);
    while (!stopping_) {
      if (stop_cv_.wait_for(lk, kProbeInterval, [this] { return stopping_; })) {
        return;
      }
      lk.unlock();
      probe();
      lk.lock();
    }
  }

  std::string host_;
  int port_ = 0;
  std::string password_;
  int db_ = 0;
  ClientOptions opts_;
  ConnectionPool pool_;

  // Metrics handles are owned by the Provider; nullptr when metrics disabled.
  metrics::Gauge* total_conns_ = nullptr;
  metrics::Gauge* idle_conns_ = nullptr;
  metrics::Histogram* ping_duration_ = nullptr;
  metrics::Gauge* up_ = nullptr;

  std::thread probe_thread_;
  std::mutex stop_mu_;
  std::condition_variable stop_cv_;
  bool stopping_ = false;
};

}  // namespace redis
}  // namespace pine
