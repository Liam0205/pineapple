#include "server/server.hpp"

#include "pine/resource.hpp"

#include <sys/stat.h>

#include <algorithm>
#include <cerrno>
#include <chrono>
#include <csignal>
#include <cstring>
#include <iostream>
#include <sstream>
#include <thread>

#include "config/json_writer.hpp"
#include "server/http_stats.hpp"

// POSIX socket headers
#include <arpa/inet.h>
#include <netinet/in.h>
#include <poll.h>
#include <sys/eventfd.h>
#include <sys/socket.h>
#include <unistd.h>

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
  if (it != ops_.end()) {
    return *it->second;
  }
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
    if (st.max_duration_ns.compare_exchange_weak(cur, ns, std::memory_order_relaxed)) {
      break;
    }
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
    if (st.max_duration_ns.compare_exchange_weak(cur, ns, std::memory_order_relaxed)) {
      break;
    }
  }
}

std::vector<std::pair<std::string, OpStatsSnapshot>> Stats::snapshot() const {
  std::lock_guard<std::mutex> lock(mu_);
  std::vector<std::pair<std::string, OpStatsSnapshot>> result;
  result.reserve(order_.size());
  for (const auto& name : order_) {
    auto it = ops_.find(name);
    if (it == ops_.end()) {
      continue;
    }
    const auto& st = *it->second;
    int64_t exec = st.exec_count.load(std::memory_order_relaxed);
    int64_t total = st.total_duration_ns.load(std::memory_order_relaxed);
    int64_t avg = exec > 0 ? total / exec : 0;
    result.push_back({name, OpStatsSnapshot{exec, st.skip_count.load(std::memory_order_relaxed),
                                            st.error_count.load(std::memory_order_relaxed), total,
                                            st.max_duration_ns.load(std::memory_order_relaxed), avg}});
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
  // Track HTTP/1.0 vs HTTP/1.1 to choose default keep-alive policy.
  // HTTP/1.1 defaults to keep-alive unless `Connection: close`; HTTP/1.0
  // defaults to close unless `Connection: keep-alive`.
  std::string http_version = "HTTP/1.1";
  bool connection_keep_alive = false;  // explicit `Connection: keep-alive` header
};

// Read all available data from socket until headers are complete, then read body.
bool read_http_request(int fd, HttpRequest& req, int64_t max_body_size, int header_timeout_seconds,
                       int body_timeout_seconds) {
  // pine-go's http.Server ReadHeaderTimeout caps the duration
  // for header reading independently of ReadTimeout. Raw sockets cannot
  // model two timeouts at once, but we can swap SO_RCVTIMEO at the
  // header boundary — short window for headers (Slowloris defense),
  // wider window for body once we know how much to expect. Both default
  // to 0 (caller passed unset) → leave timeout as caller set.
  auto apply_rcv_timeout = [fd](int seconds) {
    if (seconds <= 0) {
      return;
    }
    struct timeval to{seconds, 0};
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &to, sizeof(to));
  };
  apply_rcv_timeout(header_timeout_seconds);

  std::string buffer;
  char chunk[4096];

  // Read until we get full headers, hard cap headers size at 1MB to prevent OOM.
  // Check the cap *after* append rather than before, so a single
  // chunk that overflows the limit is reliably caught instead of letting
  // the buffer grow to `max_header_size + chunk_size` before we notice.
  const std::size_t max_header_size = 1024 * 1024;
  std::string::size_type header_end = std::string::npos;
  while (header_end == std::string::npos) {
    ssize_t n = recv(fd, chunk, sizeof(chunk), 0);
    if (n <= 0) {
      return false;
    }
    buffer.append(chunk, static_cast<size_t>(n));
    if (buffer.size() > max_header_size) {
      return false;
    }
    header_end = buffer.find("\r\n\r\n");
  }

  // Header phase complete — relax timeout to the body window before
  // reading the (possibly large) body.
  apply_rcv_timeout(body_timeout_seconds);

  // Parse request line
  auto first_line_end = buffer.find("\r\n");
  if (first_line_end == std::string::npos) {
    return false;
  }
  std::string request_line = buffer.substr(0, first_line_end);

  // "METHOD /path HTTP/1.1"
  auto space1 = request_line.find(' ');
  if (space1 == std::string::npos) {
    return false;
  }
  req.method = request_line.substr(0, space1);

  auto space2 = request_line.find(' ', space1 + 1);
  if (space2 == std::string::npos) {
    return false;
  }
  std::string uri = request_line.substr(space1 + 1, space2 - space1 - 1);
  // Capture HTTP version so handle_connection can decide
  // the default keep-alive policy (1.1 keep-alive by default, 1.0 close).
  req.http_version = request_line.substr(space2 + 1);

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
  bool has_content_length = false;
  bool invalid_content_length = false;
  std::istringstream header_stream(headers_str);
  std::string line;
  while (std::getline(header_stream, line)) {
    if (!line.empty() && line.back() == '\r') {
      line.pop_back();
    }
    if (line.empty()) {
      continue;
    }
    auto colon = line.find(':');
    if (colon == std::string::npos) {
      continue;
    }
    std::string key = line.substr(0, colon);
    std::string val = line.substr(colon + 1);
    // Trim leading whitespace from val
    while (!val.empty() && val.front() == ' ') {
      val.erase(val.begin());
    }
    // Case-insensitive header comparison
    std::string lower_key = key;
    std::transform(lower_key.begin(), lower_key.end(), lower_key.begin(), ::tolower);
    if (lower_key == "content-length") {
      has_content_length = true;
      try {
        size_t consumed = 0;
        long long parsed = std::stoll(val, &consumed);
        if (consumed != val.size() || parsed < 0) {
          invalid_content_length = true;
        } else {
          req.content_length = parsed;
        }
      } catch (...) {
        invalid_content_length = true;
      }
    } else if (lower_key == "connection") {
      std::string lower_val = val;
      std::transform(lower_val.begin(), lower_val.end(), lower_val.begin(), ::tolower);
      if (lower_val == "close") {
        req.connection_close = true;
      } else if (lower_val == "keep-alive") {
        req.connection_keep_alive = true;
      }
    }
  }

  if (invalid_content_length) {
    // Signal malformed request to caller.
    req.content_length = -2;
    req.body = "";
    return true;
  }
  (void)has_content_length;  // POST bodies without Content-Length read 0 bytes (matches existing behavior).

  // Check declared body size against the hard cap BEFORE reading.
  if (req.content_length > max_body_size) {
    req.content_length = max_body_size + 1;  // signal "too large"
    req.body = "";
    return true;
  }

  // Read body — bounded by both Content-Length and max_body_size + 1 so a
  // misbehaving client cannot stream past the cap even if its header lied.
  size_t body_start = header_end + 4;
  std::string body_so_far = buffer.substr(body_start);
  const int64_t hard_cap = max_body_size + 1;
  int64_t need = req.content_length - static_cast<int64_t>(body_so_far.size());
  while (need > 0 && static_cast<int64_t>(body_so_far.size()) < hard_cap) {
    size_t want = std::min(sizeof(chunk), static_cast<size_t>(need));
    // Never read past the hard cap.
    if (static_cast<int64_t>(body_so_far.size()) + static_cast<int64_t>(want) > hard_cap) {
      want = static_cast<size_t>(hard_cap - static_cast<int64_t>(body_so_far.size()));
    }
    ssize_t n = recv(fd, chunk, want, 0);
    if (n <= 0) {
      break;
    }
    body_so_far.append(chunk, static_cast<size_t>(n));
    need -= n;
  }
  if (static_cast<int64_t>(body_so_far.size()) > max_body_size) {
    req.content_length = max_body_size + 1;  // signal "too large"
    req.body = "";
    return true;
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
    if (amp == std::string::npos) {
      break;
    }
    pos = amp + 1;
  }
  return "";
}

std::string status_text(int code) {
  switch (code) {
    case 200:
      return "OK";
    case 400:
      return "Bad Request";
    case 405:
      return "Method Not Allowed";
    case 413:
      return "Request Entity Too Large";
    case 404:
      return "Not Found";
    case 500:
      return "Internal Server Error";
    case 503:
      return "Service Unavailable";
    default:
      return "Unknown";
  }
}

// JSON helpers — build JSON strings without depending on nlohmann
std::string json_escape(const std::string& s) {
  std::string out;
  out.reserve(s.size() + 8);
  for (char c : s) {
    switch (c) {
      case '"':
        out += "\\\"";
        break;
      case '\\':
        out += "\\\\";
        break;
      case '\n':
        out += "\\n";
        break;
      case '\r':
        out += "\\r";
        break;
      case '\t':
        out += "\\t";
        break;
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

// Convert a Variant to its JSON string representation.
std::string jsonvalue_to_string(const Variant& v) {
  if (v.is_null()) {
    return "null";
  }
  if (v.is_bool()) {
    return v.as_bool() ? "true" : "false";
  }
  if (v.is_number()) {
    double d = v.as_number();
    // Match Go's json.Encoder behavior for numbers
    if (d == static_cast<double>(static_cast<int64_t>(d)) && d >= -1e15 && d <= 1e15) {
      // Integer-like
      return std::to_string(static_cast<int64_t>(d));
    }
    char buf[64];
    int n = snprintf(buf, sizeof(buf), "%.17g", d);
    return std::string(buf, static_cast<size_t>(n));
  }
  if (v.is_string()) {
    return "\"" + json_escape(v.as_string()) + "\"";
  }
  if (v.is_array()) {
    std::string out = "[";
    bool first = true;
    for (const auto& item : v.as_array()) {
      if (!first) {
        out += ",";
      }
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
      if (!first) {
        out += ",";
      }
      first = false;
      out += "\"" + json_escape(k) + "\":" + jsonvalue_to_string(val);
    }
    out += "}";
    return out;
  }
  return "null";
}

}  // anonymous namespace

// ---- Server implementation ----

// Thread-local pointer to the current request's MiddlewareContext, so that
// send_response can record the outgoing HTTP status for HTTP-layer
// instrumentation. Set in the per-connection thread before
// dispatch, cleared after.
thread_local MiddlewareContext* t_mw_ctx = nullptr;

Server::Server() = default;
Server::~Server() {
  stop();
}

void Server::send_response(int fd, int status, const std::string& content_type, const std::string& body) {
  if (t_mw_ctx) {
    t_mw_ctx->status = status;
  }
  // Choose Connection header from the middleware context's
  // keep_alive flag, set by handle_connection's keep-alive loop. When
  // the flag is false (HTTP/1.0 by default, or `Connection: close`
  // header was present, or the keep-alive loop is on its way to close
  // anyway), emit Connection: close as before.
  const bool keep_alive = (t_mw_ctx && t_mw_ctx->keep_alive);
  std::string response = "HTTP/1.1 " + std::to_string(status) + " " + status_text(status) + "\r\n";
  response += "Content-Type: " + content_type + "\r\n";
  response += "Content-Length: " + std::to_string(body.size()) + "\r\n";
  response += keep_alive ? "Connection: keep-alive\r\n" : "Connection: close\r\n";
  response += "\r\n";
  response += body;

  size_t total = response.size();
  size_t sent = 0;
  while (sent < total) {
    ssize_t n = ::send(fd, response.data() + sent, total - sent, MSG_NOSIGNAL);
    if (n <= 0) {
      break;
    }
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

void Server::handle_execute(int client_fd, const std::string& method, const std::string& body,
                            int64_t content_length) {
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
  Variant json;
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
    std::string response =
        "{\"common\":null,\"items\":null,\"error\":\"pine: validation error: request.Common must not be "
        "nil\"}\n";
    send_json(client_fd, 400, response);
    return;
  }

  // Check if trace is requested
  bool return_trace = false;
  auto trace_it = req.common.find("_return_trace");
  if (trace_it != req.common.end() && trace_it->second.is_bool()) {
    return_trace = trace_it->second.as_bool();
  }

  // Execute with per-request arena allocation. All Variant containers
  // (object_t, array_t) allocated during execute + serialization use
  // bump-pointer allocation; the entire arena is freed at scope exit.
  std::string response;
  int status = 200;
  {
    RequestArena arena;

    // Execute (R10-2: pass client_fd so engine sees client-disconnect cancel)
    auto exec_result = execute_with_trace(req, return_trace, client_fd);

    // Hot-reload can swap the engine out between the fast-path check above
    // and the locked execute; the helper reports that via engine_not_loaded
    // and it must keep the same 503 contract as the fast path.
    if (exec_result.engine_not_loaded) {
      send_error(client_fd, 503, "engine not loaded");
      return;
    }

    // Build response JSON — match Go's executeResponse structure.
    // Go writes resp.Common/Items from result only when result != nil; an
    // engine-level error leaves them as nil maps → JSON `null`. A successful
    // run that produces an empty common/items still serializes as `{}`/`[]`.
    response = "{";

    if (!exec_result.has_result) {
      response += "\"common\":null";
      response += ",\"items\":null";
    } else {
      response += "\"common\":" + result_common_to_json(exec_result.result.common);
      response += ",\"items\":" + result_items_to_json(exec_result.result.items);
    }

    // Warnings
    if (!exec_result.warnings.empty()) {
      response += ",\"warnings\":[";
      for (size_t i = 0; i < exec_result.warnings.size(); ++i) {
        if (i > 0) {
          response += ",";
        }
        response += "\"" + json_escape(exec_result.warnings[i]) + "\"";
      }
      response += "]";
    }

    // Trace
    if (!exec_result.trace.empty()) {
      response += ",\"trace\":[";
      for (size_t i = 0; i < exec_result.trace.size(); ++i) {
        if (i > 0) {
          response += ",";
        }
        const auto& t = exec_result.trace[i];
        response += "{\"name\":\"" + json_escape(t.name) + "\"";
        response += ",\"duration_ms\":";
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
        if (t.has_input_snapshot) {
          response += ",\"input_snapshot\":" + dump_json_fast(t.input_snapshot, 0);
        }
        if (t.has_output_snapshot) {
          response += ",\"output_snapshot\":" + dump_json_fast(t.output_snapshot, 0);
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

    if (exec_result.has_error) {
      status = exec_result.is_validation_error ? 400 : 500;
    }
  }  // arena scope ends — all arena-allocated Variants freed

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
    if (i > 0) {
      ops_json += ",";
    }
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
  int64_t peak = 0;
  {
    std::shared_lock<std::shared_mutex> lock(engine_mu_);
    if (engine_) {
      peak = engine_->peak_concurrency();
    }
  }
  sched_json += ",\"peak_concurrency\":" + std::to_string(peak);
  sched_json += "}";

  // Build server JSON
  std::string server_json = "{";
  server_json += "\"reload_count\":" + std::to_string(reload_count_.load(std::memory_order_relaxed));
  server_json +=
      ",\"reload_error_count\":" + std::to_string(reload_error_count_.load(std::memory_order_relaxed));
  server_json += ",\"last_reload_duration_ns\":" +
                 std::to_string(last_reload_duration_ns_.load(std::memory_order_relaxed));
  server_json += "}";

  std::string body =
      "{\"operators\":" + ops_json + ",\"scheduler\":" + sched_json + ",\"server\":" + server_json;

  // Build http JSON (mirrors pine-go /stats.http subtree). Maps from
  // HttpStats are already lexicographically ordered (std::map) so iteration
  // order produces byte-exact JSON to match Go's encoding/json.
  if (http_stats_) {
    auto duration_snap = http_stats_->durations_snapshot();
    auto requests_snap = http_stats_->requests_snapshot();

    std::string http_json = "{\"request_duration_seconds\":{";
    bool first_dur = true;
    for (const auto& [key, bucket] : duration_snap) {
      if (!first_dur) {
        http_json += ",";
      }
      first_dur = false;
      http_json += "\"" + json_escape(key) + "\":{";
      http_json += "\"count\":" + std::to_string(bucket.count);
      http_json += ",\"sum_ns\":" + std::to_string(bucket.sum_ns);
      http_json += "}";
    }
    http_json += "},\"requests_total\":{";
    bool first_req = true;
    for (const auto& [key, count] : requests_snap) {
      if (!first_req) {
        http_json += ",";
      }
      first_req = false;
      http_json += "\"" + json_escape(key) + "\":" + std::to_string(count);
    }
    http_json += "}}";
    body += ",\"http\":" + http_json;
  }

  // Build resources JSON (mirrors pine-go /stats.resources). The collector's
  // to_json() is already key-sorted and byte-exact with the Go/Java collectors.
  {
    std::shared_lock<std::shared_mutex> lock(engine_mu_);
    if (resource_metrics_) {
      body += ",\"resources\":" + resource_metrics_->to_json();
    }
  }

  {
    std::shared_lock<std::shared_mutex> lock(engine_mu_);
    if (engine_) {
      auto custom_stats = engine_->operator_custom_stats();
      if (!custom_stats.empty()) {
        body += ",\"operator_detail\":{";
        bool first_op = true;
        for (const auto& [op_name, stats] : custom_stats) {
          if (!first_op) {
            body += ",";
          }
          first_op = false;
          body += "\"" + json_escape(op_name) + "\":{";
          bool first_stat = true;
          for (const auto& [stat_key, stat_val] : stats) {
            if (!first_stat) {
              body += ",";
            }
            first_stat = false;
            body += "\"" + json_escape(stat_key) + "\":" + std::to_string(stat_val);
          }
          body += "}";
        }
        body += "}";
      }
    }
  }

  body += "}\n";

  send_json(client_fd, 200, body);
}

void Server::handle_dag(int client_fd, const std::string& method, const std::string& query_string) {
  if (method != "GET") {
    send_error(client_fd, 405, "method not allowed");
    return;
  }

  if (!engine_) {
    send_error(client_fd, 503, "engine not loaded");
    return;
  }

  std::string format = url_decode_query_param(query_string, "format");
  if (format.empty()) {
    format = "dot";
  }

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

void Server::handle_custom_route(int client_fd, const Route& route, const RouteRequest& req) {
  // Method enforcement mirrors pine-go's routeHandler: an empty route.method
  // means "any method"; otherwise a mismatch short-circuits with the same 405
  // JSON body the built-in endpoints emit (byte-exact with send_error/405).
  if (!route.method.empty() && req.method != route.method) {
    send_error(client_fd, 405, "method not allowed");
    return;
  }

  // Run under a per-request arena like handle_execute so all Variant
  // containers allocated by ingress/execute/egress hit the bump allocator and
  // are freed together at scope exit.
  RouteResponse resp;
  {
    RequestArena arena;

    // Ingress: convert the raw request into an engine Request. A throw aborts
    // execution and surfaces to Egress as a non-empty error, mirroring
    // pine-go's Ingress returning (nil, err) → Egress(w, r, nil, err).
    Request engine_req;
    try {
      engine_req = route.ingress(req);
    } catch (const std::exception& e) {
      route.egress(resp, req, nullptr, e.what());
      send_response(client_fd, resp.status, resp.content_type, resp.body);
      return;
    }

    // Execute through execute_with_trace — the same path as the built-in
    // /execute — so scheduler run_count and per-operator exec/skip stats
    // count custom-route pipeline runs too (cross-runtime /stats parity),
    // and client-disconnect cancel works exactly like /execute. The helper
    // does the engine-null check inside its engine_mu_ shared-lock window
    // (no check-then-execute race with hot-reload) and reports it via
    // engine_not_loaded, which surfaces to Egress as an error (mirrors
    // pine-go Execute returning ErrEngineNotLoaded, which routeHandler
    // forwards to Egress with a nil result).
    ExecuteResult exec_result = execute_with_trace(engine_req, false, client_fd);
    if (exec_result.has_error) {
      route.egress(resp, req, nullptr, exec_result.error);
    } else {
      route.egress(resp, req, &exec_result.result, "");
    }
  }  // arena scope ends — all arena-allocated Variants freed

  // Egress owns status / content_type / body; the core just writes it out.
  send_response(client_fd, resp.status, resp.content_type, resp.body);
}

ExecuteResult Server::execute_with_trace(const Request& request, bool return_trace, int client_fd) {
  ExecuteResult exec_result;

  try {
    TracedResult traced;
    std::exception_ptr exec_err = nullptr;

    // R10-2: poll the client socket for disconnect and forward the
    // cancel into the engine. Without this, /execute keeps running
    // even after the client closed (waste + slow shutdown).
    // poll(POLLRDHUP|POLLHUP|POLLERR) does NOT consume bytes, so
    // it is safe to use mid-keep-alive — a queued follow-up request
    // looks like POLLIN, which we don't react to.
    std::stop_source cancel_src;
    int wake_fd = -1;
    std::thread watcher;
    if (client_fd >= 0) {
      wake_fd = ::eventfd(0, EFD_CLOEXEC | EFD_NONBLOCK);
      if (wake_fd < 0) {
        client_fd = -1;
      }
    }
    if (client_fd >= 0) {
      int wfd = wake_fd;
      watcher = std::thread([client_fd, wfd, &cancel_src]() {
        for (;;) {
          struct pollfd pfds[2]{};
          pfds[0].fd = client_fd;
#ifdef POLLRDHUP
          pfds[0].events = POLLRDHUP;
#else
          pfds[0].events = 0;
#endif
          pfds[1].fd = wfd;
          pfds[1].events = POLLIN;

          int rc = ::poll(pfds, 2, -1);
          if (rc > 0) {
            if (pfds[1].revents & POLLIN) {
              return;
            }
            if (pfds[0].revents & (POLLERR | POLLHUP
#ifdef POLLRDHUP
                                   | POLLRDHUP
#endif
                                   | POLLNVAL)) {
              cancel_src.request_stop();
              return;
            }
          }
        }
      });
    }
    {
      // Engine-null check, resource snapshot and execute share ONE engine_mu_
      // shared-lock window. reload_config moves BOTH engine_ and
      // resource_manager_ under the exclusive lock and tears the old ones
      // down after unlocking, so reading either outside this window races
      // hot-reload (unique_ptr data race / use-after-free) or pairs an old
      // resource snapshot with a new engine.
      std::shared_lock<std::shared_mutex> lock(engine_mu_);
      if (!engine_) {
        exec_result.engine_not_loaded = true;
        exec_result.has_error = true;
        exec_result.error = "engine not loaded";
      } else {
        run_count_.fetch_add(1, std::memory_order_relaxed);
        std::map<std::string, Variant> res_snap;
        if (resource_manager_) {
          res_snap = resource_manager_->snapshot();
        }
        try {
          engine_->execute_traced_into(request, res_snap, &traced, cancel_src.get_token());
        } catch (...) {
          exec_err = std::current_exception();
        }
      }
    }
    if (watcher.joinable()) {
      if (wake_fd >= 0) {
        uint64_t val = 1;
        auto ignored = ::write(wake_fd, &val, sizeof(val));
        (void)ignored;
      }
      watcher.join();
    }
    if (wake_fd >= 0) {
      ::close(wake_fd);
    }
    if (exec_result.engine_not_loaded) {
      return exec_result;
    }
    // Match Go: even on partial execution errors, we capture the Projected
    // result (items and common containing fields up to failure point) as well
    // as traces/warnings compiled up to the failure point.
    exec_result.result = std::move(traced.result);
    exec_result.warnings = std::move(traced.warnings);
    exec_result.has_result = true;

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
        if (t.has_input_snapshot) {
          te.has_input_snapshot = true;
          te.input_snapshot = t.input_snapshot;
        }
        if (t.has_output_snapshot) {
          te.has_output_snapshot = true;
          te.output_snapshot = t.output_snapshot;
        }
        exec_result.trace.push_back(std::move(te));
      }
    }

    if (exec_err) {
      std::rethrow_exception(exec_err);
    }

  } catch (const ValidationError& e) {
    exec_result.has_error = true;
    exec_result.is_validation_error = true;
    exec_result.error = e.what();
  } catch (const ExecutionError& e) {
    exec_result.has_error = true;
    exec_result.error = e.what();
  } catch (const RegistryError& e) {
    exec_result.has_error = true;
    exec_result.error = e.what();
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
  if (s) {
    s->stop();
  }
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
    auto new_config = load_config_from_file(config_.config_path);
    // Forward resolved Provider during hot-reload too, so metrics keep
    // flowing through the same sink after config swap.
    metrics::Provider* provider =
        config_.metrics_provider ? config_.metrics_provider : metrics::nop_provider();
    EngineOptions engine_opts;
    engine_opts.metrics_provider = provider;
    if (!new_config.log_prefix.empty()) {
      engine_opts.log_prefix = new_config.log_prefix;
    }
    if (config_.dag_pool_size > 0) {
      engine_opts.dag_pool_size = config_.dag_pool_size;
    }
    if (config_.shard_pool_size > 0) {
      engine_opts.shard_pool_size = config_.shard_pool_size;
    }

    // Build and start the new ResourceManager BEFORE the Engine: the Engine
    // injects the ResourceProvider into ResourceAware operators at construction
    // time, so the provider pointer must be live first. Handle-typed resources
    // (connection pools) are created here in start(). The manager writes through
    // a fresh tee into both the injected provider and a per-reload collector
    // exposed under /stats.resources.
    auto new_resource_metrics = std::make_unique<metrics::Collector>();
    auto new_resource_tee = std::make_unique<metrics::TeeProvider>(
        std::vector<metrics::Provider*>{provider, new_resource_metrics.get()});
    auto new_resource_manager = std::make_unique<resource::Manager>(new_resource_tee.get());
    new_resource_manager->load_from_config(new_config);
    new_resource_manager->validate_resource_deps(new_config);
    new_resource_manager->start();
    engine_opts.resource_provider = new_resource_manager.get();

    auto new_engine = std::make_unique<Engine>(new_config, std::move(engine_opts));
    auto new_expanded = expand_operator_sequence_with_subflows(new_config);

    std::unique_ptr<Engine> old_engine;
    std::unique_ptr<resource::Manager> old_resource_manager;
    std::unique_ptr<metrics::Provider> old_resource_tee;
    std::unique_ptr<metrics::Collector> old_resource_metrics;
    {
      std::unique_lock lock(engine_mu_);
      old_engine = std::move(engine_);
      engine_ = std::move(new_engine);
      old_resource_manager = std::move(resource_manager_);
      resource_manager_ = std::move(new_resource_manager);
      old_resource_tee = std::move(resource_tee_);
      resource_tee_ = std::move(new_resource_tee);
      old_resource_metrics = std::move(resource_metrics_);
      resource_metrics_ = std::move(new_resource_metrics);
    }
    // Retire the swapped-out engine outside the lock: close() tears down its
    // operators (e.g. Lua state pools) for parity with pine-go/pine-java; the
    // unique_ptr then frees it via RAII. No in-flight request can reference it
    // — handlers hold engine_mu_ shared for the whole execute, so all had
    // finished before we took the exclusive lock above. Stop the old manager
    // AFTER the old engine is closed so no borrowed handle outlives its pool;
    // the same engine_mu_ drain guarantees no execute still holds a borrow. The
    // old tee/collector are freed at scope exit, after the manager is stopped,
    // so no resource's metric pointer dangles during teardown.
    if (old_engine) {
      old_engine->close();
    }
    if (old_resource_manager) {
      old_resource_manager->stop();
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
    if (!running_.load(std::memory_order_acquire)) {
      break;
    }

    if (::stat(config_.config_path.c_str(), &st) != 0) {
      continue;
    }
    if (st.st_mtime <= last_mod) {
      continue;
    }

    last_mod = st.st_mtime;
    auto start = std::chrono::steady_clock::now();
    if (reload_config()) {
      auto dur = std::chrono::steady_clock::now() - start;
      reload_count_.fetch_add(1, std::memory_order_relaxed);
      last_reload_duration_ns_.store(std::chrono::duration_cast<std::chrono::nanoseconds>(dur).count(),
                                     std::memory_order_relaxed);
      if (m_reload_total_) {
        m_reload_total_->inc();
      }
      if (m_reload_duration_) {
        m_reload_duration_->observe(
            metrics::duration_seconds(std::chrono::duration_cast<std::chrono::nanoseconds>(dur)));
      }
      std::cerr << "config reloaded from " << config_.config_path << "\n";
    } else {
      reload_error_count_.fetch_add(1, std::memory_order_relaxed);
      if (m_reload_errors_) {
        m_reload_errors_->inc();
      }
    }
  }
}

int Server::run(const ServerConfig& cfg) {
  config_ = cfg;

  if (cfg.config_path.empty()) {
    std::cerr << "usage: pineapple-server -config <path-to-config.json>\n";
    return 1;
  }

  // Validate custom routes before doing any expensive setup (engine load,
  // resource manager start, socket bind). known_paths_ starts from the
  // built-in endpoints and grows as each custom route is validated, so it
  // doubles as the low-cardinality path set handed to normalize_path for HTTP
  // metrics. route_map_ gives dispatch an O(log n) exact-path lookup. Mirrors
  // pine-go's validateRoutes + mux registration in Run.
  {
    std::string route_err;
    if (!validate_routes(config_.routes, known_paths_, route_err)) {
      std::cerr << route_err << "\n";
      return 1;
    }
    route_map_.clear();
    for (const auto& route : config_.routes) {
      route_map_[route.path] = &route;
    }
  }

  // Apply HTTP metrics as innermost middleware. Standard behavior: we use
  // the configured metrics_provider, or fall back to nop_provider() so the
  // middleware chain is ALWAYS configured identically to Go/Java/Python.
  // The middleware also feeds an in-process HttpStats that surfaces through
  // GET /stats.http; timing excludes user middleware overhead.
  metrics::Provider* provider = config_.metrics_provider ? config_.metrics_provider : metrics::nop_provider();
  http_stats_ = std::make_unique<HttpStats>();
  config_.middlewares.push_back(http_metrics_middleware(provider, http_stats_.get()));

  // Hot-reload Provider metrics. Mirrors pine-go pkg/server/server.go:100-114
  // — same metric names, help text, and bucket schedule so downstream
  // Prometheus dashboards work unchanged. NopProvider returns no-op
  // pointers so the call sites stay branchless.
  m_reload_total_ =
      provider->new_counter({"pine_config_reload_total", "Total successful config reloads.", {}});
  m_reload_errors_ =
      provider->new_counter({"pine_config_reload_errors_total", "Total failed config reloads.", {}});
  m_reload_duration_ = provider->new_histogram(
      {{"pine_config_reload_duration_seconds", "Config reload duration in seconds.", {}},
       {0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0}});

  // Load engine and start resource manager if configured
  try {
    auto config = load_config_from_file(cfg.config_path);
    // Forward the resolved Provider (caller-configured or NopProvider
    // fallback) into the Engine via EngineOptions so per-operator,
    // scheduler, and DAG-level metrics reach the same Provider as the
    // http_metrics middleware. Mirrors pine-go's
    // `pine.NewEngine(cfg, pine.WithMetrics(mp))`.
    EngineOptions engine_opts;
    engine_opts.metrics_provider = provider;
    if (!config.log_prefix.empty()) {
      engine_opts.log_prefix = config.log_prefix;
    }
    if (cfg.dag_pool_size > 0) {
      engine_opts.dag_pool_size = cfg.dag_pool_size;
    }
    if (cfg.shard_pool_size > 0) {
      engine_opts.shard_pool_size = cfg.shard_pool_size;
    }

    // Build and start the ResourceManager BEFORE the Engine so the
    // ResourceProvider is live when the Engine injects it into ResourceAware
    // operators at construction time. The manager writes through a tee into
    // both the injected provider (e.g. Prometheus) and a dedicated collector
    // exposed under /stats.resources; the engine uses `provider` directly so
    // engine metrics stay out of the resources subtree.
    resource_metrics_ = std::make_unique<metrics::Collector>();
    resource_tee_ = std::make_unique<metrics::TeeProvider>(
        std::vector<metrics::Provider*>{provider, resource_metrics_.get()});
    resource_manager_ = std::make_unique<resource::Manager>(resource_tee_.get());
    resource_manager_->load_from_config(config);
    resource_manager_->validate_resource_deps(config);
    resource_manager_->start();
    engine_opts.resource_provider = resource_manager_.get();

    engine_ = std::make_unique<Engine>(config, std::move(engine_opts));
  } catch (const std::exception& e) {
    std::cerr << "failed to load engine/resources: " << e.what() << "\n";
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
  if (addr.empty()) {
    addr = ":8080";
  }
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

  // Start the config file watcher thread unless disabled. Watch defaults to
  // true; watch=false leaves watcher_thread default-constructed (not
  // joinable) so config changes require a process restart. Mirrors pine-go's
  // Config.Watch toggle. The join at shutdown is guarded by joinable().
  std::thread watcher_thread;
  if (config_.watch) {
    watcher_thread = std::thread([this]() { watch_config(); });
  }

  // Accept loop
  while (running_.load(std::memory_order_acquire)) {
    struct pollfd pfd{};
    pfd.fd = listen_fd_;
    pfd.events = POLLIN;
    int ret = poll(&pfd, 1, 500);  // 500ms timeout for shutdown check
    if (ret <= 0) {
      continue;
    }

    struct sockaddr_in client_addr{};
    socklen_t client_len = sizeof(client_addr);
    int client_fd = accept(listen_fd_, reinterpret_cast<struct sockaddr*>(&client_addr), &client_len);
    if (client_fd < 0) {
      if (running_.load(std::memory_order_acquire)) {
        // Real error, not shutdown
      }
      continue;
    }

    // Apply per-connection socket timeouts. Mirrors pine-go's
    // http.Server ReadTimeout / WriteTimeout. ReadHeaderTimeout is
    // now wired — read_http_request swaps SO_RCVTIMEO at
    // the header boundary, so the short window applies to headers
    // and the long window applies to body reads. IdleTimeout is
    // handled by the keep-alive loop.
    int rcv_secs = config_.read_timeout_seconds > 0 ? config_.read_timeout_seconds : 30;
    int snd_secs = config_.write_timeout_seconds > 0 ? config_.write_timeout_seconds : 60;
    int header_secs = config_.read_header_timeout_seconds > 0 ? config_.read_header_timeout_seconds
                                                              : rcv_secs;  // default: same as read_timeout
    int idle_secs = config_.idle_timeout_seconds > 0 ? config_.idle_timeout_seconds
                                                     : rcv_secs;  // default: same as read_timeout
    // Initial socket timeout: write side and a wide read fallback;
    // read_http_request below installs the precise header→body
    // schedule via SO_RCVTIMEO swap.
    struct timeval rcv_to{rcv_secs, 0};
    struct timeval snd_to{snd_secs, 0};
    setsockopt(client_fd, SOL_SOCKET, SO_RCVTIMEO, &rcv_to, sizeof(rcv_to));
    setsockopt(client_fd, SOL_SOCKET, SO_SNDTIMEO, &snd_to, sizeof(snd_to));

    // Handle request in a detached thread for concurrency
    in_flight_.fetch_add(1, std::memory_order_relaxed);
    std::thread([this, client_fd, header_secs, rcv_secs, idle_secs]() {
      // RAII decrement so detached threads always release the counter.
      // Wakes stop()'s drain loop via drain_cv_ so it does not have
      // to poll on a 50 ms sleep_for.
      struct InFlightGuard {
        std::atomic<int64_t>& c;
        std::mutex& mu;
        std::condition_variable& cv;
        ~InFlightGuard() {
          c.fetch_sub(1, std::memory_order_relaxed);
          std::lock_guard<std::mutex> lk(mu);
          cv.notify_all();
        }
      } guard{in_flight_, drain_mu_, drain_cv_};
      (void)guard;

      // HTTP/1.1 keep-alive loop. The first request uses
      // header_secs as the header-phase timeout; subsequent
      // requests on the same connection use idle_secs as the
      // header-phase timeout (the idle window between requests).
      // Body timeouts always use rcv_secs.
      bool first_request = true;
      while (true) {
        int hdr_to = first_request ? header_secs : idle_secs;
        first_request = false;

        HttpRequest req;
        if (!read_http_request(client_fd, req, config_.max_request_body_size, hdr_to, rcv_secs)) {
          // Either EOF, timeout, or malformed — close the connection.
          ::close(client_fd);
          return;
        }

        // Check parse status: malformed Content-Length → 400
        if (req.content_length == -2) {
          send_error(client_fd, 400, "invalid Content-Length header");
          ::close(client_fd);
          return;
        }

        // Check if content_length exceeded max
        if (req.content_length > config_.max_request_body_size) {
          send_error(client_fd, 413, "request body too large");
          ::close(client_fd);
          return;
        }

        // Decide keep-alive policy for this request. HTTP/1.1
        // defaults to keep-alive unless `Connection: close` is
        // set; HTTP/1.0 defaults to close unless
        // `Connection: keep-alive` is set. Matches pine-go's
        // http.Server semantics.
        bool keep_alive;
        if (req.http_version == "HTTP/1.0") {
          keep_alive = req.connection_keep_alive && !req.connection_close;
        } else {
          keep_alive = !req.connection_close;
        }

        // Set up middleware context + dispatch chain.
        MiddlewareContext mw_ctx;
        mw_ctx.method = req.method;
        mw_ctx.path = req.path;
        mw_ctx.normalized_path = normalize_path(req.path, known_paths_);
        mw_ctx.request_bytes = req.content_length > 0 ? req.content_length : 0;
        mw_ctx.status = 200;
        mw_ctx.keep_alive = keep_alive;

        // Innermost handler: actual route dispatch. Built-in endpoints first,
        // then custom routes, then the not-found fallback — mirroring pine-go's
        // mux where built-ins are registered before Config.Routes and "/" is
        // the catch-all. route_map_ is read-only after run() built it.
        std::function<void()> dispatch = [&]() {
          if (req.path == "/health") {
            handle_health(client_fd, req.method);
          } else if (req.path == "/execute") {
            handle_execute(client_fd, req.method, req.body, req.content_length);
          } else if (req.path == "/stats") {
            handle_stats(client_fd, req.method);
          } else if (req.path == "/dag") {
            handle_dag(client_fd, req.method, req.query_string);
          } else if (auto it = route_map_.find(req.path); it != route_map_.end()) {
            RouteRequest rr;
            rr.method = req.method;
            rr.path = req.path;
            rr.query = req.query_string;
            rr.body = req.body;
            handle_custom_route(client_fd, *it->second, rr);
          } else {
            handle_not_found(client_fd);
          }
        };

        // Apply user middlewares outer-to-inner: the first middleware
        // sees the request first, matching pine-go's loop direction.
        std::function<void()> chain = dispatch;
        const auto& mws = config_.middlewares;
        for (auto it = mws.rbegin(); it != mws.rend(); ++it) {
          Middleware mw = *it;
          std::function<void()> next = chain;
          chain = [&mw_ctx, mw, next]() { mw(mw_ctx, next); };
        }

        t_mw_ctx = &mw_ctx;
        chain();
        t_mw_ctx = nullptr;

        if (!keep_alive) {
          ::close(client_fd);
          return;
        }
        // Loop back to read the next request — read_http_request
        // will install idle_secs as the header timeout on the
        // next iteration so a permanently-idle keep-alive
        // connection is reaped.
      }
    }).detach();
  }

  g_server.store(nullptr, std::memory_order_release);
  if (watcher_thread.joinable()) {
    watcher_thread.join();
  }
  if (listen_fd_ >= 0) {
    ::close(listen_fd_);
    listen_fd_ = -1;
  }
  // Graceful drain: wait for all in-flight handler threads to finish.
  // No deadline — abandoning still-running threads is a use-after-free
  // hazard once Server members (engine_, config_, engine_mu_, stats_)
  // get torn down by ~Server(). Detached threads hold raw `this` and
  // raw references into those members; outliving the destructor would
  // touch freed storage.
  //
  // Socket read/write timeouts (config_.read_timeout_seconds etc.) bound
  // the runtime of each individual request, so this loop terminates as
  // long as no handler is genuinely stuck. If a future regression makes
  // a handler unbounded, we will deadlock here — that is louder and
  // safer than the UAF it replaces.
  std::cerr << "shutting down...\n";
  {
    std::unique_lock<std::mutex> lk(drain_mu_);
    drain_cv_.wait(lk, [this] { return in_flight_.load(std::memory_order_relaxed) == 0; });
  }
  // All in-flight handlers have finished, so the live engine has no remaining
  // references. Tear down its operators for parity with pine-go/pine-java;
  // ~Server then frees the engine via RAII.
  {
    std::unique_lock lock(engine_mu_);
    if (engine_) {
      engine_->close();
    }
    // Stop the ResourceManager after the engine is closed so handle-typed
    // resources (connection pools) are torn down only once no operator can
    // still borrow them. ~Manager would also stop(), but doing it here keeps
    // the engine-then-resources ordering explicit and observable.
    if (resource_manager_) {
      resource_manager_->stop();
    }
  }
  return 0;
}

}  // namespace server
}  // namespace pine
