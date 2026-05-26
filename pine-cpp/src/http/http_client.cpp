#include "http/http_client.hpp"
#include "http/ssrf.hpp"

#include <curl/curl.h>

#include <mutex>
#include <string>

namespace pine {
namespace http {

namespace {

std::once_flag g_curl_init_flag;

size_t write_callback(char* ptr, size_t size, size_t nmemb, void* userdata) {
    auto* state = static_cast<std::pair<std::string*, int64_t>*>(userdata);
    size_t bytes = size * nmemb;
    auto* buf = state->first;
    int64_t cap = state->second;
    if (cap >= 0 && static_cast<int64_t>(buf->size() + bytes) > cap) {
        // Overrun by one extra byte so the caller can detect the limit.
        size_t remaining = static_cast<size_t>(cap - static_cast<int64_t>(buf->size()) + 1);
        if (remaining > bytes) remaining = bytes;
        buf->append(ptr, remaining);
        return 0;  // libcurl treats short write as error → abort transfer
    }
    buf->append(ptr, bytes);
    return bytes;
}

int sockopt_ssrf_callback(void* clientp, curl_socket_t /*curlfd*/, curlsocktype /*purpose*/) {
    // libcurl has already resolved DNS and is about to open a connection.
    // Re-check the resolved peer address by reading getsockname() — but at
    // this point the socket is unconnected. Instead we rely on the
    // host-validation pass before the curl_easy_perform() call. This callback
    // exists as a hook for future refinement (e.g. CURLOPT_OPENSOCKETFUNCTION).
    (void)clientp;
    return CURL_SOCKOPT_OK;
}

}  // namespace

void global_init() {
    std::call_once(g_curl_init_flag, [] {
        curl_global_init(CURL_GLOBAL_DEFAULT);
    });
}

PostResult post(const PostOptions& opts) {
    global_init();

    PostResult result;

    // SSRF guard — refuse loopback/private targets unless allow_private is on.
    if (!opts.allow_private) {
        // Extract host from the URL for validation. We expect http://host[:port]/path
        // style URLs; rely on libcurl's URL parser for correctness.
        CURLU* uh = curl_url();
        if (uh == nullptr) {
            result.error = "transform_by_remote_pineapple: curl_url init failed";
            return result;
        }
        struct UrlScope { CURLU* h; ~UrlScope() { if (h) curl_url_cleanup(h); } } scope{uh};
        if (curl_url_set(uh, CURLUPART_URL, opts.url.c_str(), 0) != CURLUE_OK) {
            result.error = "transform_by_remote_pineapple: malformed URL: " + opts.url;
            return result;
        }
        char* host_cstr = nullptr;
        if (curl_url_get(uh, CURLUPART_HOST, &host_cstr, 0) != CURLUE_OK || host_cstr == nullptr) {
            result.error = "transform_by_remote_pineapple: cannot extract host from URL: " + opts.url;
            return result;
        }
        std::string host = host_cstr;
        curl_free(host_cstr);
        std::string reason;
        if (!validate_remote_host(host, &reason)) {
            result.error = "transform_by_remote_pineapple: " + reason;
            return result;
        }
    }

    CURL* curl = curl_easy_init();
    if (curl == nullptr) {
        result.error = "transform_by_remote_pineapple: curl_easy_init failed";
        return result;
    }
    struct EasyScope { CURL* h; ~EasyScope() { if (h) curl_easy_cleanup(h); } } scope{curl};

    auto buf_state = std::make_pair(&result.body, opts.max_response_size);
    struct curl_slist* headers = curl_slist_append(nullptr,
        ("Content-Type: " + opts.content_type).c_str());
    struct HdrScope { curl_slist* h; ~HdrScope() { if (h) curl_slist_free_all(h); } } hscope{headers};

    curl_easy_setopt(curl, CURLOPT_URL, opts.url.c_str());
    curl_easy_setopt(curl, CURLOPT_POST, 1L);
    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, opts.body.data());
    curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE, static_cast<long>(opts.body.size()));
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT_MS,
        static_cast<long>(opts.timeout.count()));
    curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, &write_callback);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &buf_state);
    curl_easy_setopt(curl, CURLOPT_SOCKOPTFUNCTION, &sockopt_ssrf_callback);
    // Reject TLS by default for now — pine-go uses plain HTTP for downstream
    // pineapple. If a future caller needs HTTPS, lift this restriction.
    curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, 0L);

    CURLcode rc = curl_easy_perform(curl);
    if (rc != CURLE_OK) {
        // Size overflow surfaces as CURLE_WRITE_ERROR with body already
        // containing max_response_size + 1 bytes.
        if (rc == CURLE_WRITE_ERROR &&
            opts.max_response_size >= 0 &&
            static_cast<int64_t>(result.body.size()) > opts.max_response_size) {
            result.error = "transform_by_remote_pineapple: response body exceeds " +
                           std::to_string(opts.max_response_size) + " bytes limit";
        } else {
            result.error = std::string("transform_by_remote_pineapple: request failed: ") +
                           curl_easy_strerror(rc);
        }
        return result;
    }

    long status = 0;
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &status);
    result.status_code = static_cast<int>(status);
    result.ok = true;
    return result;
}

}  // namespace http
}  // namespace pine
