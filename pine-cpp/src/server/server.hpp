#pragma once

#include "pine/pine.hpp"
#include "pine/resource.hpp"

#include <atomic>
#include <chrono>
#include <functional>
#include <map>
#include <mutex>
#include <shared_mutex>
#include <condition_variable>
#include <string>
#include <vector>

namespace pine {
namespace server {

// Per-operator stats, thread-safe via atomics.
struct OpStats {
    std::atomic<int64_t> exec_count{0};
    std::atomic<int64_t> skip_count{0};
    std::atomic<int64_t> error_count{0};
    std::atomic<int64_t> total_duration_ns{0};
    std::atomic<int64_t> max_duration_ns{0};
};

// Snapshot of per-operator stats for serialization.
struct OpStatsSnapshot {
    int64_t exec_count = 0;
    int64_t skip_count = 0;
    int64_t error_count = 0;
    int64_t total_duration_ns = 0;
    int64_t max_duration_ns = 0;
    int64_t avg_duration_ns = 0;
};

// Trace entry for a single operator execution.
struct TraceEntry {
    std::string name;
    double duration_ms = 0.0;
    bool skipped = false;
    bool has_input_snapshot = false;
    JsonValue input_snapshot;
    bool has_output_snapshot = false;
    JsonValue output_snapshot;
};

// Result of an engine execution, including trace and warnings.
struct ExecuteResult {
    Result result;
    std::vector<std::string> warnings;
    std::vector<TraceEntry> trace;
    std::string error;
    bool is_validation_error = false;
    bool has_error = false;
    // Whether `result` was populated by the engine. Mirrors pine-go's
    // `result != nil` check: on engine-level errors the result is "nil",
    // and the response must serialize common/items as JSON `null` (not `{}`/`[]`).
    bool has_result = false;
};

// Stats accumulator for per-operator metrics.
class Stats {
public:
    // Pre-register operator names so they appear from startup.
    void pre_init(const std::vector<std::string>& names);

    void record_exec(const std::string& name, std::chrono::nanoseconds duration);
    void record_skip(const std::string& name);
    void record_error(const std::string& name, std::chrono::nanoseconds duration);

    // Returns a snapshot ordered by the pre-init sequence.
    std::vector<std::pair<std::string, OpStatsSnapshot>> snapshot() const;

private:
    OpStats& get_or_create(const std::string& name);

    mutable std::mutex mu_;
    std::map<std::string, std::unique_ptr<OpStats>> ops_;
    std::vector<std::string> order_;  // insertion order
};

// Scheduler-level stats.
struct SchedulerStatsSnapshot {
    int64_t run_count = 0;
    int64_t peak_concurrency = 0;
};

// Per-request context passed through the middleware chain. `status` is set by
// `send_response` (via thread-local capture) and visible to middleware after
// `next()` returns. Mirrors pine-go's `statusRecorder`.
struct MiddlewareContext {
    std::string method;
    std::string path;          // raw request path
    std::string normalized_path; // /execute, /health, /stats, /dag, or "_other"
    int64_t request_bytes = 0;
    int status = 200;          // populated by inner handler
};

// User-supplied middleware. Middleware MUST call `next()` exactly once unless
// it intends to short-circuit the request. Outer-to-inner composition matches
// pine-go's Config.Middlewares semantics.
using Middleware = std::function<void(MiddlewareContext&, const std::function<void()>& next)>;

class HttpStats;

// Builds the HTTP-layer metrics middleware (requests_total + duration
// histogram). The server core installs one of these automatically as the
// innermost middleware on every run (with NopProvider tied off when no
// provider is configured), mirroring pine-go's "always-on" behavior. The
// overload taking an HttpStats* also feeds the in-process /stats.http
// accumulator, which is what the server core uses internally.
Middleware http_metrics_middleware(pine::metrics::Provider* provider);
Middleware http_metrics_middleware(pine::metrics::Provider* provider, HttpStats* http_stats);

// Server configuration.
struct ServerConfig {
    std::string config_path;
    std::string addr = ":8080";
    int64_t max_request_body_size = 10 * 1024 * 1024;  // 10 MB
    // HTTP socket timeouts, seconds. 0 means "use default":
    //   read_header: 10s, read: 30s, write: 60s, idle: 120s.
    // Mirrors pine-go's pineapple-server -read-header-timeout / -read-timeout
    // / -write-timeout / -idle-timeout flags + server.Config defaults.
    int read_header_timeout_seconds = 0;
    int read_timeout_seconds = 0;
    int write_timeout_seconds = 0;
    int idle_timeout_seconds = 0;

    // HTTP middlewares applied outer-to-inner (first sees request first).
    // Mirrors pine-go server.Config.Middlewares.
    std::vector<Middleware> middlewares;

    // Optional metrics provider for HTTP-layer instrumentation. nullptr → no-op.
    pine::metrics::Provider* metrics_provider = nullptr;
};

// Minimal HTTP server for pine-cpp.
class Server {
public:
    Server();
    ~Server();

    // Run starts the server and blocks until stopped.
    int run(const ServerConfig& cfg);

    // Stop signals the server to shut down.
    void stop();

private:
    // HTTP handlers
    void handle_health(int client_fd, const std::string& method);
    void handle_execute(int client_fd, const std::string& method,
                        const std::string& body, int64_t content_length);
    void handle_stats(int client_fd, const std::string& method);
    void handle_dag(int client_fd, const std::string& method,
                    const std::string& query_string);
    void handle_not_found(int client_fd);

    // Execute with stats tracking and tracing.
    ExecuteResult execute_with_trace(const Request& request, bool return_trace);

    // Config file watcher thread (polls mtime every 2s).
    void watch_config();

    // Reload engine from config file.
    bool reload_config();

    // Response helpers
    static void send_response(int fd, int status, const std::string& content_type,
                              const std::string& body);
    static void send_json(int fd, int status, const std::string& json_body);
    static void send_error(int fd, int status, const std::string& message);

    // State (engine_ protected by engine_mu_ for hot-reload)
    mutable std::shared_mutex engine_mu_;
    std::unique_ptr<Engine> engine_;
    std::unique_ptr<resource::Manager> resource_manager_;
    std::unique_ptr<Stats> stats_;
    ServerConfig config_;
    std::atomic<bool> running_{false};
    int listen_fd_ = -1;

    // Scheduler-level stats
    std::atomic<int64_t> run_count_{0};

    // In-flight request counter for graceful shutdown.
    // Mirrors pine-go http.Server.Shutdown waiting for active conns.
    std::atomic<int64_t> in_flight_{0};
    // Drain cv lets stop() wait on in_flight_ → 0 without sleep-for polling.
    // The request thread guard decrements in_flight_ then notifies the cv.
    // P1-P5.
    std::mutex drain_mu_;
    std::condition_variable drain_cv_;

    // Hot-reload stats
    std::atomic<int64_t> reload_count_{0};
    std::atomic<int64_t> reload_error_count_{0};
    std::atomic<int64_t> last_reload_duration_ns_{0};

    // HTTP request stats fed by the default http_metrics middleware.
    std::unique_ptr<HttpStats> http_stats_;
};

}  // namespace server
}  // namespace pine
