#pragma once

#include <chrono>
#include <cstdint>
#include <string>

namespace pine {
namespace http {

struct PostOptions {
    std::string url;
    std::string body;
    std::string content_type = "application/json";
    std::chrono::milliseconds timeout = std::chrono::seconds(5);
    int64_t max_response_size = 10 * 1024 * 1024;
    // When false, refuse to connect to loopback / private / link-local
    // addresses. The actual host validation happens in ssrf.hpp; this flag
    // only toggles the IP-resolution guard inside the libcurl callback.
    bool allow_private = false;
};

// Result of an HTTP request. `ok == false` means the request itself failed
// (DNS / connect / TLS / timeout / SSRF block / size cap). `ok == true` means
// a response was received; check `status_code`.
struct PostResult {
    bool ok = false;
    int status_code = 0;
    std::string body;
    std::string error;  // populated when ok == false
};

// One-shot POST. Thread-safe: caller owns the lifetime of input strings.
PostResult post(const PostOptions& opts);

// Initialize libcurl globally. Idempotent and thread-safe-after-first-call.
// The first call should happen before any threads start using post().
void global_init();

}  // namespace http
}  // namespace pine
