#include "http/ssrf.hpp"

#include <arpa/inet.h>
#include <netdb.h>
#include <netinet/in.h>
#include <sys/socket.h>

#include <cstring>

namespace pine {
namespace http {

namespace {

bool ipv4_is_private(uint32_t ip_host_order) {
    // 127.0.0.0/8       loopback
    // 10.0.0.0/8        private
    // 172.16.0.0/12     private
    // 192.168.0.0/16    private
    // 169.254.0.0/16    link-local
    // 0.0.0.0/8         "this network" — also unsafe to dial
    uint8_t a = (ip_host_order >> 24) & 0xff;
    uint8_t b = (ip_host_order >> 16) & 0xff;
    if (a == 127 || a == 10 || a == 0) return true;
    if (a == 172 && (b & 0xf0) == 16) return true;
    if (a == 192 && b == 168) return true;
    if (a == 169 && b == 254) return true;
    return false;
}

bool ipv6_is_private(const struct in6_addr& a) {
    // ::1 loopback
    if (IN6_IS_ADDR_LOOPBACK(&a)) return true;
    if (IN6_IS_ADDR_LINKLOCAL(&a)) return true;
    if (IN6_IS_ADDR_SITELOCAL(&a)) return true;
    if (IN6_IS_ADDR_UNSPECIFIED(&a)) return true;
    // fc00::/7 — unique local
    if ((a.s6_addr[0] & 0xfe) == 0xfc) return true;
    // IPv4-mapped: ::ffff:a.b.c.d → reuse IPv4 rules
    if (IN6_IS_ADDR_V4MAPPED(&a)) {
        uint32_t ip = (static_cast<uint32_t>(a.s6_addr[12]) << 24) |
                      (static_cast<uint32_t>(a.s6_addr[13]) << 16) |
                      (static_cast<uint32_t>(a.s6_addr[14]) << 8) |
                      (static_cast<uint32_t>(a.s6_addr[15]));
        return ipv4_is_private(ip);
    }
    return false;
}

}  // namespace

bool ip_literal_is_private(const std::string& ip) {
    struct in_addr v4;
    if (inet_pton(AF_INET, ip.c_str(), &v4) == 1) {
        return ipv4_is_private(ntohl(v4.s_addr));
    }
    struct in6_addr v6;
    if (inet_pton(AF_INET6, ip.c_str(), &v6) == 1) {
        return ipv6_is_private(v6);
    }
    return false;
}

bool host_is_private(const std::string& host, std::string* error_out) {
    if (host.empty() || host == "localhost") {
        if (error_out) *error_out = "host \"" + host + "\" is not allowed (private/loopback)";
        return true;
    }
    // If it parses as an IP literal, check directly.
    {
        struct in_addr v4;
        if (inet_pton(AF_INET, host.c_str(), &v4) == 1) {
            if (ipv4_is_private(ntohl(v4.s_addr))) {
                if (error_out) *error_out = "host \"" + host + "\" is a private/loopback address";
                return true;
            }
            return false;
        }
        struct in6_addr v6;
        if (inet_pton(AF_INET6, host.c_str(), &v6) == 1) {
            if (ipv6_is_private(v6)) {
                if (error_out) *error_out = "host \"" + host + "\" is a private/loopback address";
                return true;
            }
            return false;
        }
    }
    // Symbolic hostname — best-effort DNS lookup. A failure does not classify
    // as "private"; the dial-time guard is the real defense.
    struct addrinfo hints{};
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    struct addrinfo* res = nullptr;
    int rc = getaddrinfo(host.c_str(), nullptr, &hints, &res);
    if (rc != 0 || res == nullptr) {
        return false;
    }
    bool found_private = false;
    std::string offending;
    for (auto* p = res; p != nullptr; p = p->ai_next) {
        char buf[INET6_ADDRSTRLEN] = {0};
        if (p->ai_family == AF_INET) {
            auto* sa = reinterpret_cast<sockaddr_in*>(p->ai_addr);
            inet_ntop(AF_INET, &sa->sin_addr, buf, sizeof(buf));
            if (ipv4_is_private(ntohl(sa->sin_addr.s_addr))) {
                found_private = true;
                offending = buf;
                break;
            }
        } else if (p->ai_family == AF_INET6) {
            auto* sa = reinterpret_cast<sockaddr_in6*>(p->ai_addr);
            inet_ntop(AF_INET6, &sa->sin6_addr, buf, sizeof(buf));
            if (ipv6_is_private(sa->sin6_addr)) {
                found_private = true;
                offending = buf;
                break;
            }
        }
    }
    freeaddrinfo(res);
    if (found_private) {
        if (error_out) *error_out = "host \"" + host + "\" resolves to private address " + offending;
        return true;
    }
    return false;
}

bool validate_remote_host(const std::string& host, std::string* error_out) {
    std::string reason;
    if (host_is_private(host, &reason)) {
        if (error_out) *error_out = reason;
        return false;
    }
    return true;
}

}  // namespace http
}  // namespace pine
