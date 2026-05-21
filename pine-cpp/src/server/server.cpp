#include "server/server.hpp"

#include <algorithm>
#include <cerrno>
#include <chrono>
#include <cstring>
#include <iostream>
#include <sstream>
#include <thread>
#include <csignal>
#include <sys/stat.h>

// POSIX socket headers
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>
#include <poll.h>

namespace pine {
namespace server {

// ---- Stats implementation ----

void Stats::pre_init(const std::vector<std::string>& names) {
    std::lock_guard<std::mutex> lock(mu_);
    for (const auto& name : names) {
        if (ops_.find(name) == ops_.end()) {
            ops_[name] = std::make_unique<OpStats>();
            order_.push_back(name);
        }
    }
}

OpStats& Stats::get_or_create(const std::string& name) {
    std::lock_guard<std::mutex> lock(mu_);
    auto it = ops_.find(name);
    if (it != ops_.end()) return *it->second;
    auto ptr = std::make_unique<OpStats>();
    auto& ref = *ptr;
    ops_[name] = std::move(ptr);
    order_.push_back(name);
    return ref;
}

void Stats::record_exec(const std::string& name, std::chrono::nanoseconds duration) {
    auto& st = get_or_create(name);
    st.exec_count.fetch_add(1, std::memory_order_relaxed);
    int64_t ns = duration.count();
    st.total_duration_ns.fetch_add(ns, std::memory_order_relaxed);
    // CAS loop for max
    int64_t cur = st.max_duration_ns.load(std::memory_order_relaxed);
    while (ns > cur) {
        if (st.max_duration_ns.compare_exchange_weak(cur, ns, std::memory_order_relaxed)) break;
    }
}

void Stats::record_skip(const std::string& name) {
    auto& st = get_or_create(name);
    st.skip_count.fetch_add(1, std::memory_order_relaxed);
}

void Stats::record_error(const std::string& name, std::chrono::nanoseconds duration) {
    auto& st = get_or_create(name);
    st.error_count.fetch_add(1, std::memory_order_relaxed);
    int64_t ns = duration.count();
    st.total_duration_ns.fetch_add(ns, std::memory_order_relaxed);
    int64_t cur = st.max_duration_ns.load(std::memory_order_relaxed);
    while (ns > cur) {
        if (st.max_duration_ns.compare_exchange_weak(cur, ns, std::memory_order_relaxed)) break;
    }
}

std::vector<std::pair<std::string, OpStatsSnapshot>> Stats::snapshot() const {
    std::lock_guard<std::mutex> lock(mu_);
    std::vector<std::pair<std::string, OpStatsSnapshot>> result;
    result.reserve(order_.size());
    for (const auto& name : order_) {
        auto it = ops_.find(name);
        if (it == ops_.end()) continue;
        const auto& st = *it->second;
        int64_t exec = st.exec_count.load(std::memory_order_relaxed);
        int64_t total = st.total_duration_ns.load(std::memory_order_relaxed);
        int64_t avg = exec > 0 ? total / exec : 0;
        result.push_back({name, OpStatsSnapshot{
            exec,
            st.skip_count.load(std::memory_order_relaxed),
            st.error_count.load(std::memory_order_relaxed),
            total,
            st.max_duration_ns.load(std::memory_order_relaxed),
            avg
        }});
    }
    return result;
}

// ---- HTTP parsing helpers ----

namespace {

struct HttpRequest {
    std::string method;
    std::string path;
    std::string query_string;
    std::string body;
    int64_t content_length = -1;
    bool connection_close = false;
};

// Read all available data from socket until headers are complete, then read body.
bool read_http_request(int fd, HttpRequest& req, int64_t max_body_size) {
    std::string buffer;
    char chunk[4096];

    // Read until we get full headers
    std::string::size_type header_end = std::string::npos;
    while (header_end == std::string::npos) {
        ssize_t n = recv(fd, chunk, sizeof(chunk), 0);
        if (n <= 0) return false;
        buffer.append(chunk, static_cast<size_t>(n));
        header_end = buffer.find("\r\n\r\n");
    }

    // Parse request line
    auto first_line_end = buffer.find("\r\n");
    if (first_line_end == std::string::npos) return false;
    std::string request_line = buffer.substr(0, first_line_end);

    // "METHOD /path HTTP/1.1"
    auto space1 = request_line.find(' ');
    if (space1 == std::string::npos) return false;
    req.method = request_line.substr(0, space1);

    auto space2 = request_line.find(' ', space1 + 1);
    if (space2 == std::string::npos) return false;
    std::string uri = request_line.substr(space1 + 1, space2 - space1 - 1);

    auto q = uri.find('?');
    if (q != std::string::npos) {
        req.path = uri.substr(0, q);
        req.query_string = uri.substr(q + 1);
    } else {
        req.path = uri;
    }

    // Parse headers
    std::string headers_str = buffer.substr(first_line_end + 2, header_end - first_line_end - 2);
    req.content_length = 0;
    std::istringstream header_stream(headers_str);
    std::string line;
    while (std::getline(header_stream, line)) {
        if (!line.empty() && line.back() == '\r') line.pop_back();
        if (line.empty()) continue;
        auto colon = line.find(':');
        if (colon == std::string::npos) continue;
        std::string key = line.substr(0, colon);
        std::string val = line.substr(colon + 1);
        // Trim leading whitespace from val
        while (!val.empty() && val.front() == ' ') val.erase(val.begin());
        // Case-insensitive header comparison
        std::string lower_key = key;
        std::transform(lower_key.begin(), lower_key.end(), lower_key.begin(), ::tolower);
        if (lower_key == "content-length") {
            req.content_length = std::stoll(val);
        } else if (lower_key == "connection") {
            std::string lower_val = val;
            std::transform(lower_val.begin(), lower_val.end(), lower_val.begin(), ::tolower);
            if (lower_val == "close") req.connection_close = true;
        }
    }

    // Check body size limit BEFORE reading body
    if (req.content_length > max_body_size) {
        // Signal to caller that body is too large.
        // We still need to consume or discard remaining data.
        req.content_length = max_body_size + 1;  // signal "too large"
        req.body = "";
        return true;
    }

    // Read body
    size_t body_start = header_end + 4;
    std::string body_so_far = buffer.substr(body_start);
    int64_t need = req.content_length - static_cast<int64_t>(body_so_far.size());
    while (need > 0) {
        ssize_t n = recv(fd, chunk, std::min(sizeof(chunk), static_cast<size_t>(need)), 0);
        if (n <= 0) break;
        body_so_far.append(chunk, static_cast<size_t>(n));
        need -= n;
    }
    req.body = std::move(body_so_far);
    return true;
}

std::string url_decode_query_param(const std::string& qs, const std::string& key) {
    std::string search = key + "=";
    std::string::size_type pos = 0;
    while (pos < qs.size()) {
        auto amp = qs.find('&', pos);
        std::string param = (amp == std::string::npos) ? qs.substr(pos) : qs.substr(pos, amp - pos);
        if (param.size() >= search.size() && param.substr(0, search.size()) == search) {
            return param.substr(search.size());
        }
        if (amp == std::string::npos) break;
        pos = amp + 1;
    }
    return "";
}

std::string status_text(int code) {
    switch (code) {
        case 200: return "OK";
        case 400: return "Bad Request";
        case 405: return "Method Not Allowed";
        case 413: return "Request Entity Too Large";
        case 404: return "Not Found";
        case 500: return "Internal Server Error";
        case 503: return "Service Unavailable";
        default: return "Unknown";
    }
}

// JSON helpers — build JSON strings without depending on nlohmann
std::string json_escape(const std::string& s) {
    std::string out;
    out.reserve(s.size() + 8);
    for (char c : s) {
        switch (c) {
            case '"': out += "\\\""; break;
            case '\\': out += "\\\\"; break;
            case '\n': out += "\\n"; break;
            case '\r': out += "\\r"; break;
            case '\t': out += "\\t"; break;
            default:
                if (static_cast<unsigned char>(c) < 0x20) {
                    char buf[8];
                    snprintf(buf, sizeof(buf), "\\u%04x", static_cast<unsigned char>(c));
                    out += buf;
                } else {
                    out += c;
                }
        }
    }
    return out;
}

// Convert a JsonValue to its JSON string representation.
std::string jsonvalue_to_string(const JsonValue& v) {
    if (v.is_null()) return "null";
    if (v.is_bool()) return v.as_bool() ? "true" : "false";
    if (v.is_number()) {
        double d = v.as_number();
        // Match Go's json.Encoder behavior for numbers
        if (d == static_cast<double>(static_cast<int64_t>(d)) &&
            d >= -1e15 && d <= 1e15) {
            // Integer-like
            return std::to_string(static_cast<int64_t>(d));
        }
        char buf[64];
        int n = snprintf(buf, sizeof(buf), "%.17g", d);
        return std::string(buf, static_cast<size_t>(n));
    }
    if (v.is_string()) return "\"" + json_escape(v.as_string()) + "\"";
    if (v.is_array()) {
        std::string out = "[";
        bool first = true;
        for (const auto& item : v.as_array()) {
            if (!first) out += ",";
            first = false;
            out += jsonvalue_to_string(item);
        }
        out += "]";
        return out;
    }
    if (v.is_object()) {
        std::string out = "{";
        bool first = true;
        for (const auto& [k, val] : v.as_object()) {
            if (!first) out += ",";
            first = false;
            out += "\"" + json_escape(k) + "\":" + jsonvalue_to_string(val);
        }
        out += "}";
        return out;
    }
    return "null";
}

// Build a JSON object string from a map<string, JsonValue>
std::string map_to_json(const std::map<std::string, JsonValue>& m) {
    std::string out = "{";
    bool first = true;
    for (const auto& [k, v] : m) {
        if (!first) out += ",";
        first = false;
        out += "\"" + json_escape(k) + "\":" + jsonvalue_to_string(v);
    }
    out += "}";
    return out;
}

// Build items JSON array
std::string items_to_json(const std::vector<std::map<std::string, JsonValue>>& items) {
    std::string out = "[";
    for (size_t i = 0; i < items.size(); ++i) {
        if (i > 0) out += ",";
        out += map_to_json(items[i]);
    }
    out += "]";
    return out;
}

}  // anonymous namespace

// ---- Server implementation ----

Server::Server() = default;
Server::~Server() {
    stop();
}

void Server::send_response(int fd, int status, const std::string& content_type,
                           const std::string& body) {
    std::string response = "HTTP/1.1 " + std::to_string(status) + " " + status_text(status) + "\r\n";
    response += "Content-Type: " + content_type + "\r\n";
    response += "Content-Length: " + std::to_string(body.size()) + "\r\n";
    response += "Connection: close\r\n";
    response += "\r\n";
    response += body;

    size_t total = response.size();
    size_t sent = 0;
    while (sent < total) {
        ssize_t n = ::send(fd, response.data() + sent, total - sent, MSG_NOSIGNAL);
        if (n <= 0) break;
        sent += static_cast<size_t>(n);
    }
}

void Server::send_json(int fd, int status, const std::string& json_body) {
    send_response(fd, status, "application/json", json_body);
}

void Server::send_error(int fd, int status, const std::string& message) {
    std::string body = "{\"error\":\"" + json_escape(message) + "\"}\n";
    send_json(fd, status, body);
}

void Server::handle_health(int client_fd, const std::string& method) {
    if (method != "GET") {
        send_error(client_fd, 405, "method not allowed");
        return;
    }
    send_json(client_fd, 200, "{\"status\":\"ok\"}\n");
}

void Server::handle_execute(int client_fd, const std::string& method,
                            const std::string& body, int64_t content_length) {
    if (method != "POST") {
        send_error(client_fd, 405, "method not allowed");
        return;
    }

    if (!engine_) {
        send_error(client_fd, 503, "engine not loaded");
        return;
    }

    // Check body size
    if (content_length > config_.max_request_body_size) {
        send_error(client_fd, 413, "request body too large");
        return;
    }

    // Parse JSON
    JsonValue json;
    try {
        json = parse_json(body);
    } catch (const std::exception& e) {
        std::string msg = "invalid request: " + std::string(e.what());
        send_error(client_fd, 400, msg);
        return;
    }

    if (!json.is_object()) {
        send_error(client_fd, 400, "invalid request: expected JSON object");
        return;
    }

    // Build Request
    Request req;
    const auto& obj = json.as_object();
    bool has_common = false;

    // Parse common
    auto common_it = obj.find("common");
    if (common_it != obj.end()) {
        has_common = true;
        if (common_it->second.is_object()) {
            req.common = common_it->second.as_object();
        }
        // null common is treated as nil → validation error below
        if (common_it->second.is_null()) {
            has_common = false;
        }
    }

    // Parse items
    auto items_it = obj.find("items");
    if (items_it != obj.end() && items_it->second.is_array()) {
        for (const auto& item : items_it->second.as_array()) {
            if (item.is_object()) {
                req.items.push_back(item.as_object());
            } else {
                req.items.push_back({});
            }
        }
    }

    // Match Go validation: common must not be nil
    if (!has_common) {
        // Return error matching Go behavior
        // Go: resp.Error = err.Error() where err is ValidationError with "request.Common must not be nil"
        // Go: errors.As(err, &ve) → true → 400
        // Go: resp.Common is nil, resp.Items is nil → JSON null
        std::string response = "{\"common\":null,\"items\":null,\"error\":\"pine: validation error: request.Common must not be nil\"}\n";
        send_json(client_fd, 400, response);
        return;
    }

    // Check if trace is requested
    bool return_trace = false;
    auto trace_it = req.common.find("_return_trace");
    if (trace_it != req.common.end() && trace_it->second.is_bool()) {
        return_trace = trace_it->second.as_bool();
    }

    // Execute
    auto exec_result = execute_with_trace(req, return_trace);

    // Build response JSON — match Go's executeResponse structure.
    // Go always emits "common" and "items" (even if nil → null).
    // When there's an error with no result, Go writes null for both.
    std::string response = "{";

    if (exec_result.has_error && exec_result.result.common.empty() && exec_result.result.items.empty()) {
        // Match Go: nil result → null common/items
        response += "\"common\":null";
        response += ",\"items\":null";
    } else {
        response += "\"common\":" + map_to_json(exec_result.result.common);
        response += ",\"items\":" + items_to_json(exec_result.result.items);
    }

    // Warnings
    if (!exec_result.warnings.empty()) {
        response += ",\"warnings\":[";
        for (size_t i = 0; i < exec_result.warnings.size(); ++i) {
            if (i > 0) response += ",";
            response += "\"" + json_escape(exec_result.warnings[i]) + "\"";
        }
        response += "]";
    }

    // Trace
    if (!exec_result.trace.empty()) {
        response += ",\"trace\":[";
        for (size_t i = 0; i < exec_result.trace.size(); ++i) {
            if (i > 0) response += ",";
            const auto& t = exec_result.trace[i];
            response += "{\"name\":\"" + json_escape(t.name) + "\"";
            response += ",\"duration_ms\":" ;
            // Format duration_ms to match Go's encoding:
            // Go uses float64 → json.Encoder which outputs minimal representation.
            char dur_buf[64];
            if (t.duration_ms == 0.0) {
                response += "0";
            } else {
                int n = snprintf(dur_buf, sizeof(dur_buf), "%g", t.duration_ms);
                response.append(dur_buf, static_cast<size_t>(n));
            }
            if (t.skipped) {
                response += ",\"skipped\":true";
            }
            response += "}";
        }
        response += "]";
    }

    // Error
    if (exec_result.has_error) {
        response += ",\"error\":\"" + json_escape(exec_result.error) + "\"";
    }

    response += "}\n";

    int status = 200;
    if (exec_result.has_error) {
        status = exec_result.is_validation_error ? 400 : 500;
    }

    send_json(client_fd, status, response);
}

void Server::handle_stats(int client_fd, const std::string& method) {
    if (method != "GET") {
        send_error(client_fd, 405, "method not allowed");
        return;
    }

    if (!stats_) {
        send_error(client_fd, 503, "engine not loaded");
        return;
    }

    auto ops_snapshot = stats_->snapshot();

    // Build operators JSON, preserving pipeline order
    std::string ops_json = "{";
    for (size_t i = 0; i < ops_snapshot.size(); ++i) {
        if (i > 0) ops_json += ",";
        const auto& [name, st] = ops_snapshot[i];
        ops_json += "\"" + json_escape(name) + "\":{";
        ops_json += "\"exec_count\":" + std::to_string(st.exec_count);
        ops_json += ",\"skip_count\":" + std::to_string(st.skip_count);
        ops_json += ",\"error_count\":" + std::to_string(st.error_count);
        ops_json += ",\"total_duration_ns\":" + std::to_string(st.total_duration_ns);
        ops_json += ",\"max_duration_ns\":" + std::to_string(st.max_duration_ns);
        ops_json += ",\"avg_duration_ns\":" + std::to_string(st.avg_duration_ns);
        ops_json += "}";
    }
    ops_json += "}";

    // Build scheduler JSON
    std::string sched_json = "{";
    sched_json += "\"run_count\":" + std::to_string(run_count_.load(std::memory_order_relaxed));
    sched_json += ",\"peak_concurrency\":0";
    sched_json += "}";

    // Build server JSON
    std::string server_json = "{";
    server_json += "\"reload_count\":" + std::to_string(reload_count_.load(std::memory_order_relaxed));
    server_json += ",\"reload_error_count\":" + std::to_string(reload_error_count_.load(std::memory_order_relaxed));
    server_json += ",\"last_reload_duration_ns\":" + std::to_string(last_reload_duration_ns_.load(std::memory_order_relaxed));
    server_json += "}";

    std::string body = "{\"operators\":" + ops_json +
                       ",\"scheduler\":" + sched_json +
                       ",\"server\":" + server_json + "}\n";

    send_json(client_fd, 200, body);
}

void Server::handle_dag(int client_fd, const std::string& method,
                        const std::string& query_string) {
    if (method != "GET") {
        send_error(client_fd, 405, "method not allowed");
        return;
    }

    if (!engine_) {
        send_error(client_fd, 503, "engine not loaded");
        return;
    }

    std::string format = url_decode_query_param(query_string, "format");
    if (format.empty()) format = "dot";

    int collapse = 0;
    std::string collapse_str = url_decode_query_param(query_string, "collapse");
    if (!collapse_str.empty()) {
        try {
            collapse = std::stoi(collapse_str);
            if (collapse < 0) {
                send_error(client_fd, 400, "collapse must be a non-negative integer");
                return;
            }
        } catch (...) {
            send_error(client_fd, 400, "collapse must be a non-negative integer");
            return;
        }
    }

    std::string output;
    try {
        std::shared_lock<std::shared_mutex> lock(engine_mu_);
        output = engine_->render_dag(format, collapse);
    } catch (const ValidationError& e) {
        send_error(client_fd, 400, e.what());
        return;
    } catch (const std::exception& e) {
        send_error(client_fd, 400, e.what());
        return;
    }

    std::string content_type;
    if (format == "dot") {
        content_type = "text/vnd.graphviz; charset=utf-8";
    } else {
        content_type = "text/plain; charset=utf-8";
    }

    send_response(client_fd, 200, content_type, output);
}

void Server::handle_not_found(int client_fd) {
    send_error(client_fd, 404, "not found");
}

ExecuteResult Server::execute_with_trace(const Request& request, bool return_trace) {
    ExecuteResult exec_result;
    run_count_.fetch_add(1, std::memory_order_relaxed);

    static const std::map<std::string, JsonValue> empty_resources;

    try {
        TracedResult traced;
        {
            std::shared_lock<std::shared_mutex> lock(engine_mu_);
            traced = engine_->execute_traced(request, empty_resources);
        }
        exec_result.result = std::move(traced.result);
        exec_result.warnings = std::move(traced.warnings);

        for (const auto& t : traced.trace) {
            auto dur_ns = std::chrono::nanoseconds(t.duration_us * 1000);
            if (t.skipped) {
                stats_->record_skip(t.name);
            } else {
                stats_->record_exec(t.name, dur_ns);
            }

            if (return_trace) {
                TraceEntry te;
                te.name = t.name;
                te.duration_ms = static_cast<double>(t.duration_us) / 1000.0;
                te.skipped = t.skipped;
                exec_result.trace.push_back(std::move(te));
            }
        }

    } catch (const ValidationError& e) {
        exec_result.has_error = true;
        exec_result.is_validation_error = true;
        exec_result.error = std::string("pine: validation error: ") + e.what();
    } catch (const ExecutionError& e) {
        exec_result.has_error = true;
        // Go wraps as "pine: execution error in operator \"name\": inner_err"
        // C++ already includes operator name in the message, so just add prefix
        exec_result.error = std::string("pine: execution error: ") + e.what();
    } catch (const RegistryError& e) {
        exec_result.has_error = true;
        exec_result.error = std::string("pine: registry error: ") + e.what();
    } catch (const std::exception& e) {
        exec_result.has_error = true;
        exec_result.error = e.what();
    }

    return exec_result;
}

// Global pointer for signal handler
static std::atomic<Server*> g_server{nullptr};

static void signal_handler(int) {
    auto* s = g_server.load(std::memory_order_relaxed);
    if (s) s->stop();
}

void Server::stop() {
    running_.store(false, std::memory_order_release);
    if (listen_fd_ >= 0) {
        ::shutdown(listen_fd_, SHUT_RDWR);
        ::close(listen_fd_);
        listen_fd_ = -1;
    }
}

bool Server::reload_config() {
    try {
        auto new_engine = std::make_unique<Engine>(Engine::from_file(config_.config_path));
        auto new_config = load_config_from_file(config_.config_path);
        auto new_expanded = expand_operator_sequence_with_subflows(new_config);

        {
            std::unique_lock lock(engine_mu_);
            engine_ = std::move(new_engine);
        }

        stats_->pre_init(new_expanded.sequence);
        return true;
    } catch (const std::exception& e) {
        std::cerr << "config reload failed: " << e.what() << "\n";
        return false;
    }
}

void Server::watch_config() {
    struct stat st{};
    time_t last_mod = 0;
    if (::stat(config_.config_path.c_str(), &st) == 0) {
        last_mod = st.st_mtime;
    }

    while (running_.load(std::memory_order_acquire)) {
        std::this_thread::sleep_for(std::chrono::seconds(2));
        if (!running_.load(std::memory_order_acquire)) break;

        if (::stat(config_.config_path.c_str(), &st) != 0) continue;
        if (st.st_mtime <= last_mod) continue;

        last_mod = st.st_mtime;
        auto start = std::chrono::steady_clock::now();
        if (reload_config()) {
            auto dur = std::chrono::steady_clock::now() - start;
            reload_count_.fetch_add(1, std::memory_order_relaxed);
            last_reload_duration_ns_.store(
                std::chrono::duration_cast<std::chrono::nanoseconds>(dur).count(),
                std::memory_order_relaxed);
            std::cerr << "config reloaded from " << config_.config_path << "\n";
        } else {
            reload_error_count_.fetch_add(1, std::memory_order_relaxed);
        }
    }
}

int Server::run(const ServerConfig& cfg) {
    config_ = cfg;

    if (cfg.config_path.empty()) {
        std::cerr << "usage: pineapple-server -config <path-to-config.json>\n";
        return 1;
    }

    // Load engine
    try {
        engine_ = std::make_unique<Engine>(Engine::from_file(cfg.config_path));
    } catch (const std::exception& e) {
        std::cerr << "failed to load engine: " << e.what() << "\n";
        return 1;
    }

    // Initialize stats with operator names
    stats_ = std::make_unique<Stats>();
    // We need the expanded sequence for pre-init.
    // Re-load config to get the sequence (Engine doesn't expose it directly).
    try {
        auto config = load_config_from_file(cfg.config_path);
        auto expanded = expand_operator_sequence_with_subflows(config);
        stats_->pre_init(expanded.sequence);
    } catch (...) {
        // If we can't get the sequence, stats will still work dynamically
    }

    std::cerr << "engine loaded from " << cfg.config_path << "\n";

    // Parse address
    int port = 8080;
    std::string addr = cfg.addr;
    if (addr.empty()) addr = ":8080";
    auto colon = addr.rfind(':');
    if (colon != std::string::npos) {
        port = std::stoi(addr.substr(colon + 1));
    }

    // Create socket
    listen_fd_ = socket(AF_INET, SOCK_STREAM, 0);
    if (listen_fd_ < 0) {
        std::cerr << "socket() failed: " << strerror(errno) << "\n";
        return 1;
    }

    int optval = 1;
    setsockopt(listen_fd_, SOL_SOCKET, SO_REUSEADDR, &optval, sizeof(optval));
    setsockopt(listen_fd_, SOL_SOCKET, SO_REUSEPORT, &optval, sizeof(optval));

    struct sockaddr_in server_addr{};
    server_addr.sin_family = AF_INET;
    server_addr.sin_addr.s_addr = INADDR_ANY;
    server_addr.sin_port = htons(static_cast<uint16_t>(port));

    if (bind(listen_fd_, reinterpret_cast<struct sockaddr*>(&server_addr), sizeof(server_addr)) < 0) {
        std::cerr << "bind() failed on port " << port << ": " << strerror(errno) << "\n";
        ::close(listen_fd_);
        listen_fd_ = -1;
        return 1;
    }

    if (listen(listen_fd_, 128) < 0) {
        std::cerr << "listen() failed: " << strerror(errno) << "\n";
        ::close(listen_fd_);
        listen_fd_ = -1;
        return 1;
    }

    running_.store(true, std::memory_order_release);

    // Set up signal handling
    g_server.store(this, std::memory_order_release);
    struct sigaction sa{};
    sa.sa_handler = signal_handler;
    sigemptyset(&sa.sa_mask);
    sa.sa_flags = 0;
    sigaction(SIGINT, &sa, nullptr);
    sigaction(SIGTERM, &sa, nullptr);

    std::cerr << "listening on " << addr << "\n";

    // Start config file watcher thread
    std::thread watcher_thread([this]() { watch_config(); });

    // Accept loop
    while (running_.load(std::memory_order_acquire)) {
        struct pollfd pfd{};
        pfd.fd = listen_fd_;
        pfd.events = POLLIN;
        int ret = poll(&pfd, 1, 500);  // 500ms timeout for shutdown check
        if (ret <= 0) continue;

        struct sockaddr_in client_addr{};
        socklen_t client_len = sizeof(client_addr);
        int client_fd = accept(listen_fd_, reinterpret_cast<struct sockaddr*>(&client_addr), &client_len);
        if (client_fd < 0) {
            if (running_.load(std::memory_order_acquire)) {
                // Real error, not shutdown
            }
            continue;
        }

        // Handle request in a detached thread for concurrency
        std::thread([this, client_fd]() {
            HttpRequest req;
            if (!read_http_request(client_fd, req, config_.max_request_body_size)) {
                ::close(client_fd);
                return;
            }

            // Check if content_length exceeded max
            if (req.content_length > config_.max_request_body_size) {
                send_error(client_fd, 413, "request body too large");
                ::close(client_fd);
                return;
            }

            // Route
            if (req.path == "/health") {
                handle_health(client_fd, req.method);
            } else if (req.path == "/execute") {
                handle_execute(client_fd, req.method, req.body, req.content_length);
            } else if (req.path == "/stats") {
                handle_stats(client_fd, req.method);
            } else if (req.path == "/dag") {
                handle_dag(client_fd, req.method, req.query_string);
            } else {
                handle_not_found(client_fd);
            }

            ::close(client_fd);
        }).detach();
    }

    g_server.store(nullptr, std::memory_order_release);
    watcher_thread.join();
    if (listen_fd_ >= 0) {
        ::close(listen_fd_);
        listen_fd_ = -1;
    }
    std::cerr << "shutting down...\n";
    return 0;
}

}  // namespace server
}  // namespace pine
