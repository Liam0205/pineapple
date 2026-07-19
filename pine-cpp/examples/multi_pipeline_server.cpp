// Multi-pipeline server example — one process, several pipelines, each bound
// to its own endpoint with its own log prefix.
//
// Layout:
//
//   POST /api/feed    -> feed.json    (log_prefix "[feed] ")
//   POST /api/search  -> search.json  (log_prefix "[search] ")
//   POST /execute     -> 410 Gone     (legacy endpoint, deliberately retired)
//
// pine-cpp's bundled Server is not a public library API, so this example
// embeds the ENGINE layer directly (include/pine/pine.hpp): one pine::Engine
// per pipeline, plus a deliberately tiny blocking HTTP loop the application
// owns. The same pattern drops into any C++ HTTP framework (Drogon, Pistache,
// oatpp) by calling engine.execute() from your handlers. Engine::execute is
// concurrency-safe; production servers should also add hot-reload and
// graceful drain (see src/server/server.cpp for the reference shape).
//
// Since issue #172 log_prefix is engine-scoped: lines emitted while the feed
// pipeline executes (observe_log, [pine-debug]) carry "[feed] " while search
// lines carry "[search] ", concurrently, in one process.
//
// /execute is kept only as a tombstone: it answers 410 Gone and points
// callers at the named endpoints.
//
// Build (from pine-cpp/, requires the configured build dir):
//
//   cmake --build build --target multi_pipeline_server -j8
//
// Run & try:
//
//   ./build/multi_pipeline_server ../pine-go/examples/multi-pipeline/feed.json
//       ../pine-go/examples/multi-pipeline/search.json 8080   (one command line)
//   curl -s -X POST localhost:8080/api/feed   -d '{"common":{"user_id":"u1"}}'
//   curl -s -X POST localhost:8080/api/search -d '{"common":{"query":"tech"}}'
//   curl -si -X POST localhost:8080/execute   -d '{}'   # 410 Gone

#include "pine/pine.hpp"

#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

#include <cerrno>
#include <csignal>
#include <cstring>
#include <iostream>
#include <map>
#include <memory>
#include <sstream>
#include <string>

namespace {

pine::Request parse_request(const std::string& body) {
  pine::Request request;
  const auto root = pine::parse_json(body).as_object();
  if (auto it = root.find("common"); it != root.end()) {
    for (const auto& [key, value] : it->second.as_object()) {
      request.common[key] = value;
    }
  }
  if (auto it = root.find("items"); it != root.end()) {
    for (const auto& item : it->second.as_array()) {
      pine::Variant::object_t row;
      for (const auto& [key, value] : item.as_object()) {
        row[key] = value;
      }
      request.items.push_back(std::move(row));
    }
  }
  return request;
}

void send_response(int fd, int status, const std::string& body) {
  const char* text = status == 200   ? "OK"
                     : status == 400 ? "Bad Request"
                     : status == 404 ? "Not Found"
                     : status == 405 ? "Method Not Allowed"
                     : status == 410 ? "Gone"
                                     : "Internal Server Error";
  std::ostringstream resp;
  resp << "HTTP/1.1 " << status << " " << text << "\r\n"
       << "Content-Type: application/json\r\n"
       << "Content-Length: " << body.size() << "\r\n"
       << "Connection: close\r\n\r\n"
       << body;
  const std::string out = resp.str();
  // MSG_NOSIGNAL is mandatory on every pine-cpp raw-socket write path
  // (llmdoc/must/conventions.md): a client that disconnects mid-response
  // would otherwise raise SIGPIPE and kill the whole process. Loop until
  // all bytes are written — a single send may be short.
  size_t sent = 0;
  while (sent < out.size()) {
    ssize_t n = ::send(fd, out.data() + sent, out.size() - sent, MSG_NOSIGNAL);
    if (n < 0 && errno == EINTR) {
      continue;
    }
    if (n <= 0) {
      break;  // peer gone; nothing sensible left to do for this response
    }
    sent += static_cast<size_t>(n);
  }
}

std::string json_error(const std::string& message) {
  // Route the message through the Variant serializer: exception text can
  // contain quotes (ValidationError quotes field names) or control
  // characters, and hand-concatenating it would produce invalid JSON.
  pine::Variant::object_t obj;
  obj["error"] = pine::Variant(message);
  std::string out = pine::dump_json(pine::Variant(std::move(obj)), 0);
  if (out.empty() || out.back() != '\n') {
    out += '\n';
  }
  return out;
}

// Adapts one HTTP request to one embedded engine. execute() is
// concurrency-safe; each engine's diagnostics carry its own log_prefix.
void handle_pipeline(int fd, pine::Engine& engine, const std::string& method, const std::string& body) {
  if (method != "POST") {
    send_response(fd, 405, json_error("method not allowed"));
    return;
  }
  pine::Request request;
  try {
    request = parse_request(body);
  } catch (const std::exception&) {
    send_response(fd, 400, json_error("invalid request body"));
    return;
  }
  try {
    pine::Result result = engine.execute(request);
    send_response(fd, 200, pine::result_to_json(result));
  } catch (const pine::ValidationError& e) {
    send_response(fd, 400, json_error(e.what()));
  } catch (const std::exception& e) {
    send_response(fd, 500, json_error(e.what()));
  }
}

volatile std::sig_atomic_t g_stop = 0;

}  // namespace

int main(int argc, char** argv) {
  const std::string feed_config = argc > 1 ? argv[1] : "feed.json";
  const std::string search_config = argc > 2 ? argv[2] : "search.json";
  const int port = argc > 3 ? std::atoi(argv[3]) : 8080;

  // One embedded engine per pipeline. Each config declares its own
  // log_prefix; since issue #172 the prefix is an engine-instance member,
  // so both engines below log under different prefixes in one process.
  std::unique_ptr<pine::Engine> feed;
  std::unique_ptr<pine::Engine> search;
  try {
    feed = std::make_unique<pine::Engine>(pine::load_config_from_file(feed_config));
    search = std::make_unique<pine::Engine>(pine::load_config_from_file(search_config));
  } catch (const std::exception& e) {
    std::cerr << "error creating engines: " << e.what() << "\n";
    return 1;
  }

  int listen_fd = ::socket(AF_INET, SOCK_STREAM, 0);
  int reuse = 1;
  ::setsockopt(listen_fd, SOL_SOCKET, SO_REUSEADDR, &reuse, sizeof(reuse));
  sockaddr_in addr{};
  addr.sin_family = AF_INET;
  addr.sin_addr.s_addr = htonl(INADDR_ANY);
  addr.sin_port = htons(static_cast<uint16_t>(port));
  if (::bind(listen_fd, reinterpret_cast<sockaddr*>(&addr), sizeof(addr)) != 0 ||
      ::listen(listen_fd, 16) != 0) {
    std::cerr << "failed to listen on :" << port << "\n";
    return 1;
  }
  std::signal(SIGINT, [](int) { g_stop = 1; });
  std::cout << "listening on :" << port << "\n";

  // Minimal single-threaded accept loop — enough to demonstrate routing;
  // real deployments should use a proper HTTP framework or the bundled
  // pineapple-server shape (per-connection threads, timeouts, drain).
  while (!g_stop) {
    int fd = ::accept(listen_fd, nullptr, nullptr);
    if (fd < 0) {
      break;
    }
    std::string raw(64 * 1024, '\0');
    ssize_t n = ::read(fd, raw.data(), raw.size());
    if (n <= 0) {
      ::close(fd);
      continue;
    }
    raw.resize(static_cast<size_t>(n));

    std::istringstream head(raw.substr(0, raw.find("\r\n")));
    std::string method, path;
    head >> method >> path;
    const auto body_pos = raw.find("\r\n\r\n");
    const std::string body = body_pos == std::string::npos ? "" : raw.substr(body_pos + 4);

    if (path == "/api/feed") {
      handle_pipeline(fd, *feed, method, body);
    } else if (path == "/api/search") {
      handle_pipeline(fd, *search, method, body);
    } else if (path == "/execute") {
      // Legacy endpoint: kept so old callers get a clear migration signal
      // instead of a generic 404, but it no longer runs any pipeline.
      send_response(fd, 410, json_error("endpoint retired: use /api/feed or /api/search"));
    } else {
      send_response(fd, 404, json_error("not found"));
    }
    ::close(fd);
  }

  ::close(listen_fd);
  feed->close();
  search->close();
  return 0;
}
