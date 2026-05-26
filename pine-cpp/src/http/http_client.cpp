#include "http/http_client.hpp"
#include "http/ssrf.hpp"

#include <curl/curl.h>

#include <sys/socket.h>
#include <sys/types.h>
#include <unistd.h>

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

// Dial-time SSRF guard. libcurl invokes this just before opening a socket
// to the resolved peer; we get the actual IP that will be dialed (not the
// init-time DNS result), so this defeats DNS rebinding — even if an
// attacker-controlled hostname returned a public IP during host validation
// and later flips DNS to 169.254.169.254 / 127.0.0.1, the dial-time check
// rejects it. Returning CURL_SOCKET_BAD aborts the transfer cleanly.
//
// `clientp` carries the caller's `allow_private` flag; when set, we skip
// the check so local testing still works.
curl_socket_t opensocket_ssrf_callback(void* clientp,
                                        curlsocktype /*purpose*/,
                                        struct curl_sockaddr* address) {
    if (address == nullptr) return CURL_SOCKET_BAD;
    bool allow_private = (clientp != nullptr) && *static_cast<bool*>(clientp);
    if (!allow_private && sockaddr_is_private(&address->addr)) {
        return CURL_SOCKET_BAD;
    }
    return ::socket(address->family, address->socktype, address->protocol);
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

    bool allow_private_flag = opts.allow_private;

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
    // Dial-time SSRF guard — defeats DNS rebinding by checking the actual
    // peer IP libcurl chose, not the one we resolved during init-time host
    // validation.
    curl_easy_setopt(curl, CURLOPT_OPENSOCKETFUNCTION, &opensocket_ssrf_callback);
    curl_easy_setopt(curl, CURLOPT_OPENSOCKETDATA, &allow_private_flag);
    // Disable redirect-follow to keep the SSRF guard authoritative.
    // libcurl's OPENSOCKETFUNCTION fires on redirects too, but disabling
    // follow makes the security boundary more explicit for auditors.
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
        } else if (rc == CURLE_COULDNT_CONNECT || rc == CURLE_COULDNT_RESOLVE_HOST) {
            // CURL_SOCKET_BAD from the open-socket callback surfaces here as
            // a "couldn't connect"; relabel it so the cause is obvious.
            result.error = std::string("transform_by_remote_pineapple: request failed: ") +
                           curl_easy_strerror(rc) +
                           " (peer rejected by SSRF dial-time guard if hostname resolved to a private address)";
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
