#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/column_frame.hpp"
#include "pine/pine.hpp"
#include "http/ssrf.hpp"

#include <doctest/doctest.h>

#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

#include <atomic>
#include <chrono>
#include <cstring>
#include <map>
#include <string>
#include <thread>
#include <vector>

namespace {

// Minimal blocking HTTP/1.1 mock server. Reads one request, sends a single
// canned response, then closes. Used to exercise the operator's plain-HTTP
// happy path on the loopback interface.
class MockServer {
public:
    explicit MockServer(std::string response_body, int status = 200,
                        std::chrono::milliseconds delay = std::chrono::milliseconds(0))
        : status_(status), body_(std::move(response_body)), delay_(delay) {
        fd_ = socket(AF_INET, SOCK_STREAM, 0);
        REQUIRE(fd_ >= 0);
        int yes = 1;
        setsockopt(fd_, SOL_SOCKET, SO_REUSEADDR, &yes, sizeof(yes));
        sockaddr_in addr{};
        addr.sin_family = AF_INET;
        addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
        addr.sin_port = 0;
        REQUIRE(bind(fd_, reinterpret_cast<sockaddr*>(&addr), sizeof(addr)) == 0);
        socklen_t len = sizeof(addr);
        REQUIRE(getsockname(fd_, reinterpret_cast<sockaddr*>(&addr), &len) == 0);
        port_ = ntohs(addr.sin_port);
        REQUIRE(listen(fd_, 4) == 0);
        thread_ = std::thread([this] { run(); });
    }

    ~MockServer() {
        stopping_ = true;
        if (fd_ >= 0) {
            shutdown(fd_, SHUT_RDWR);
            close(fd_);
            fd_ = -1;
        }
        if (thread_.joinable()) thread_.join();
    }

    int port() const { return port_; }
    std::string last_request() const { return last_request_; }

private:
    void run() {
        sockaddr_in peer{};
        socklen_t plen = sizeof(peer);
        int client = accept(fd_, reinterpret_cast<sockaddr*>(&peer), &plen);
        if (client < 0) return;
        if (delay_.count() > 0) std::this_thread::sleep_for(delay_);
        char buf[8192];
        ssize_t n = recv(client, buf, sizeof(buf) - 1, 0);
        if (n > 0) {
            buf[n] = '\0';
            last_request_ = std::string(buf, static_cast<size_t>(n));
        }
        std::string status_text;
        switch (status_) {
            case 200: status_text = "OK"; break;
            case 400: status_text = "Bad Request"; break;
            case 500: status_text = "Internal Server Error"; break;
            default: status_text = "Unknown"; break;
        }
        std::string resp = "HTTP/1.1 " + std::to_string(status_) + " " + status_text + "\r\n";
        resp += "Content-Type: application/json\r\n";
        resp += "Content-Length: " + std::to_string(body_.size()) + "\r\n";
        resp += "Connection: close\r\n\r\n";
        resp += body_;
        ssize_t off = 0;
        while (off < static_cast<ssize_t>(resp.size())) {
            ssize_t w = send(client, resp.data() + off, resp.size() - off, 0);
            if (w <= 0) break;
            off += w;
        }
        shutdown(client, SHUT_RDWR);
        close(client);
    }

    int fd_ = -1;
    int port_ = 0;
    int status_;
    std::string body_;
    std::chrono::milliseconds delay_;
    std::thread thread_;
    std::atomic<bool> stopping_{false};
    std::string last_request_;
};

pine::OperatorConfig make_cfg(int port, bool allow_private = true,
                              bool fail_on_error = true,
                              double timeout = 5.0) {
    pine::OperatorConfig cfg;
    cfg.name = "remote";
    cfg.type_name = "transform_by_remote_pineapple";
    cfg.params = pine::JsonValue(pine::JsonValue::object_t{
        {"host", pine::JsonValue("127.0.0.1")},
        {"port", pine::JsonValue(static_cast<double>(port))},
        {"endpoint", pine::JsonValue("/execute")},
        {"allow_private", pine::JsonValue(allow_private)},
        {"fail_on_error", pine::JsonValue(fail_on_error)},
        {"timeout", pine::JsonValue(timeout)},
    });
    cfg.metadata.common_input = {"a"};
    cfg.metadata.common_output = {"b"};
    cfg.metadata.item_input = {"x"};
    cfg.metadata.item_output = {"y"};
    return cfg;
}

std::unique_ptr<pine::Operator> create_op() {
    const auto* entry = pine::registry_entry("transform_by_remote_pineapple");
    REQUIRE(entry != nullptr);
    return entry->factory();
}

pine::ColumnFrame build_frame() {
    std::map<std::string, pine::JsonValue> common{{"a", pine::JsonValue("hello")}};
    std::vector<std::map<std::string, pine::JsonValue>> items{
        {{"x", pine::JsonValue("v1")}},
        {{"x", pine::JsonValue("v2")}},
    };
    return pine::ColumnFrame(std::move(common), std::move(items));
}

// Build an OperatorInput from a frame using the standard remote config metadata.
pine::OperatorInput build_input_from_frame(pine::ColumnFrame& frame, const pine::OperatorConfig& cfg) {
    auto spec = pine::compute_input_field_spec(cfg);
    return pine::build_operator_input(frame, cfg.name, spec);
}

}  // namespace

TEST_CASE("remote_pineapple: ssrf rejects localhost / loopback") {
    using pine::http::host_is_private;
    using pine::http::ip_literal_is_private;

    CHECK(host_is_private("localhost"));
    CHECK(host_is_private(""));
    // Case-insensitive: LOCALHOST / LocalHost variants must all reject.
    CHECK(host_is_private("LOCALHOST"));
    CHECK(host_is_private("LocalHost"));
    CHECK(host_is_private("localHOST"));
    CHECK(ip_literal_is_private("127.0.0.1"));
    CHECK(ip_literal_is_private("10.1.2.3"));
    CHECK(ip_literal_is_private("172.16.0.1"));
    CHECK(ip_literal_is_private("192.168.1.1"));
    CHECK(ip_literal_is_private("169.254.1.1"));
    CHECK(ip_literal_is_private("::1"));
    CHECK(ip_literal_is_private("fc00::1"));
    CHECK_FALSE(ip_literal_is_private("8.8.8.8"));
    CHECK_FALSE(ip_literal_is_private("1.1.1.1"));
}

TEST_CASE("remote_pineapple: ssrf rejects obfuscated IPv4 literals") {
    using pine::http::host_is_private;
    // Hex form: 0x7f000001 = 127.0.0.1
    CHECK(host_is_private("0x7f000001"));
    CHECK(host_is_private("0X7F000001"));
    // 32-bit integer form: 2130706433 = 127.0.0.1
    CHECK(host_is_private("2130706433"));
    // Short-form IPv4: 127.1 = 127.0.0.1 in BSD parser
    CHECK(host_is_private("127.1"));
    CHECK(host_is_private("127.0.1"));
    CHECK(host_is_private("10.1"));
    // Real hostnames must still pass through (the obfuscation rejector
    // only fires on shapes that look numeric — not "google.com").
    std::string reason;
    CHECK_FALSE(host_is_private("example.com", &reason));
}

TEST_CASE("remote_pineapple: sockaddr_is_private dial-time guard") {
    using pine::http::sockaddr_is_private;
    sockaddr_in in4{};
    in4.sin_family = AF_INET;
    inet_pton(AF_INET, "127.0.0.1", &in4.sin_addr);
    CHECK(sockaddr_is_private(reinterpret_cast<sockaddr*>(&in4)));

    inet_pton(AF_INET, "169.254.169.254", &in4.sin_addr);
    CHECK(sockaddr_is_private(reinterpret_cast<sockaddr*>(&in4)));

    inet_pton(AF_INET, "8.8.8.8", &in4.sin_addr);
    CHECK_FALSE(sockaddr_is_private(reinterpret_cast<sockaddr*>(&in4)));

    sockaddr_in6 in6{};
    in6.sin6_family = AF_INET6;
    inet_pton(AF_INET6, "::1", &in6.sin6_addr);
    CHECK(sockaddr_is_private(reinterpret_cast<sockaddr*>(&in6)));

    inet_pton(AF_INET6, "2606:4700:4700::1111", &in6.sin6_addr);
    CHECK_FALSE(sockaddr_is_private(reinterpret_cast<sockaddr*>(&in6)));

    CHECK_FALSE(sockaddr_is_private(nullptr));
}

TEST_CASE("remote_pineapple: ssrf private range coverage (RFC 6890 / 6598)") {
    using pine::http::ip_literal_is_private;
    // CGN — RFC 6598
    CHECK(ip_literal_is_private("100.64.0.0"));
    CHECK(ip_literal_is_private("100.96.0.10"));
    CHECK(ip_literal_is_private("100.127.255.255"));
    CHECK_FALSE(ip_literal_is_private("100.128.0.0"));  // outside /10
    CHECK_FALSE(ip_literal_is_private("100.63.255.255"));
    // Multicast / reserved / broadcast
    CHECK(ip_literal_is_private("224.0.0.1"));
    CHECK(ip_literal_is_private("239.255.255.255"));
    CHECK(ip_literal_is_private("240.0.0.0"));
    CHECK(ip_literal_is_private("255.255.255.255"));
    // TEST-NET / documentation / benchmark / 6to4
    CHECK(ip_literal_is_private("192.0.0.0"));
    CHECK(ip_literal_is_private("192.0.2.42"));
    CHECK(ip_literal_is_private("192.88.99.1"));
    CHECK(ip_literal_is_private("198.18.0.0"));
    CHECK(ip_literal_is_private("198.19.255.255"));
    CHECK(ip_literal_is_private("198.51.100.5"));
    CHECK(ip_literal_is_private("203.0.113.5"));
    // Real public addresses remain dialable
    CHECK_FALSE(ip_literal_is_private("8.8.8.8"));
    CHECK_FALSE(ip_literal_is_private("1.1.1.1"));
    CHECK_FALSE(ip_literal_is_private("203.0.114.1"));   // just past TEST-NET-3
    CHECK_FALSE(ip_literal_is_private("198.20.0.0"));    // just past benchmark
}

TEST_CASE("remote_pineapple: ssrf IPv6 extended coverage") {
    using pine::http::ip_literal_is_private;
    // Multicast (ff00::/8)
    CHECK(ip_literal_is_private("ff02::1"));
    CHECK(ip_literal_is_private("ff00::"));
    // Discard prefix (100::/64)
    CHECK(ip_literal_is_private("100::"));
    CHECK(ip_literal_is_private("100::1"));
    CHECK_FALSE(ip_literal_is_private("100:1::"));  // not in 100::/64
    // Documentation (2001:db8::/32)
    CHECK(ip_literal_is_private("2001:db8::"));
    CHECK(ip_literal_is_private("2001:db8:ffff::1"));
    CHECK_FALSE(ip_literal_is_private("2001:db9::"));
    // Public IPv6 remains dialable
    CHECK_FALSE(ip_literal_is_private("2606:4700:4700::1111"));
    CHECK_FALSE(ip_literal_is_private("2a00:1450:4001:830::200e"));
}

TEST_CASE("remote_pineapple: init rejects loopback when allow_private=false") {
    auto op = create_op();
    pine::OperatorConfig cfg = make_cfg(12345, /*allow_private=*/false);
    CHECK_THROWS_AS(op->init(cfg), pine::ExecutionError);
}

TEST_CASE("remote_pineapple: happy path maps response fields") {
    std::string body = R"({"common":{"b":"world"},"items":[{"y":"r1"},{"y":"r2"}]})";
    MockServer srv(body, 200);

    auto op = create_op();
    auto cfg = make_cfg(srv.port());
    op->init(cfg);

    auto frame = build_frame();
    pine::OperatorOutput out;
    auto input = build_input_from_frame(frame, cfg);
    op->execute(input, out);

    const auto& cw = out.common_writes();
    REQUIRE(cw.count("b") == 1);
    CHECK(cw.at("b").as_string() == "world");

    const auto& iw = out.item_writes();
    REQUIRE(iw.size() == 2);
    std::map<int, std::string> by_idx;
    for (const auto& w : iw) {
        if (w.field == "y") by_idx[w.index] = w.value.as_string();
    }
    CHECK(by_idx.at(0) == "r1");
    CHECK(by_idx.at(1) == "r2");

    // Verify request body included our local common/item values, keyed by the
    // local field names (no common_request/item_request override).
    // R13-1: dump_json(value, 0) now uses compact format (no space after colon).
    auto req = srv.last_request();
    CHECK(req.find("\"a\":\"hello\"") != std::string::npos);
    CHECK(req.find("\"x\":\"v1\"") != std::string::npos);
    CHECK(req.find("\"x\":\"v2\"") != std::string::npos);
}

TEST_CASE("remote_pineapple: HTTP 500 throws when fail_on_error=true") {
    MockServer srv(R"({"error":"boom"})", 500);
    auto op = create_op();
    auto cfg = make_cfg(srv.port(), /*allow_private=*/true, /*fail_on_error=*/true);
    op->init(cfg);
    auto frame = build_frame();
    pine::OperatorOutput out;
    auto input = build_input_from_frame(frame, cfg);
    CHECK_THROWS_AS(op->execute(input, out), pine::ExecutionError);
}

TEST_CASE("remote_pineapple: HTTP 500 emits warning when fail_on_error=false") {
    MockServer srv(R"({"error":"boom"})", 500);
    auto op = create_op();
    auto cfg = make_cfg(srv.port(), /*allow_private=*/true, /*fail_on_error=*/false);
    op->init(cfg);
    auto frame = build_frame();
    pine::OperatorOutput out;
    auto input = build_input_from_frame(frame, cfg);
    op->execute(input, out);
    CHECK(out.warning().find("HTTP 500") != std::string::npos);
}

TEST_CASE("remote_pineapple: downstream error field surfaces as warning") {
    MockServer srv(R"({"common":{},"items":[],"error":"downstream broke"})", 200);
    auto op = create_op();
    auto cfg = make_cfg(srv.port(), /*allow_private=*/true, /*fail_on_error=*/false);
    op->init(cfg);
    auto frame = build_frame();
    pine::OperatorOutput out;
    auto input = build_input_from_frame(frame, cfg);
    op->execute(input, out);
    CHECK(out.warning().find("downstream error: downstream broke") != std::string::npos);
}

TEST_CASE("remote_pineapple: timeout produces request-failed warning") {
    MockServer srv(R"({"common":{}})", 200, std::chrono::milliseconds(500));
    auto op = create_op();
    auto cfg = make_cfg(srv.port(), /*allow_private=*/true,
                        /*fail_on_error=*/false, /*timeout=*/0.05);
    op->init(cfg);
    auto frame = build_frame();
    pine::OperatorOutput out;
    auto input = build_input_from_frame(frame, cfg);
    op->execute(input, out);
    CHECK(out.warning().find("request failed") != std::string::npos);
}
