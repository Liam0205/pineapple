#pragma once

#include "pine/pine.hpp"

#include <atomic>
#include <chrono>
#include <functional>
#include <map>
#include <mutex>
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
};

// Result of an engine execution, including trace and warnings.
struct ExecuteResult {
    Result result;
    std::vector<std::string> warnings;
    std::vector<TraceEntry> trace;
    std::string error;
    bool is_validation_error = false;
    bool has_error = false;
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

// Server configuration.
struct ServerConfig {
    std::string config_path;
    std::string addr = ":8080";
    int64_t max_request_body_size = 10 * 1024 * 1024;  // 10 MB
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

    // Response helpers
    static void send_response(int fd, int status, const std::string& content_type,
                              const std::string& body);
    static void send_json(int fd, int status, const std::string& json_body);
    static void send_error(int fd, int status, const std::string& message);

    // State
    std::unique_ptr<Engine> engine_;
    std::unique_ptr<Stats> stats_;
    ServerConfig config_;
    std::atomic<bool> running_{false};
    int listen_fd_ = -1;

    // Scheduler-level stats
    std::atomic<int64_t> run_count_{0};
};

}  // namespace server
}  // namespace pine
