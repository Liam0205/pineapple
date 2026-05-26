#include "http/ssrf.hpp"

#include <arpa/inet.h>
#include <netdb.h>
#include <netinet/in.h>
#include <sys/socket.h>

#include <algorithm>
#include <cctype>
#include <cstring>

namespace pine {
namespace http {

namespace {

bool ipv4_is_private(uint32_t ip_host_order) {
    // Documented ranges from RFC 6890 / RFC 6598 / RFC 5735 / RFC 3927 etc.
    // Reject anything not safe to dial from a production HTTP client.
    //
    //   0.0.0.0/8         "this network"                  RFC 1122
    //   10.0.0.0/8        private use                     RFC 1918
    //   100.64.0.0/10     carrier-grade NAT (CGN)         RFC 6598
    //   127.0.0.0/8       loopback                        RFC 1122
    //   169.254.0.0/16    link-local                      RFC 3927
    //   172.16.0.0/12     private use                     RFC 1918
    //   192.0.0.0/24      IETF protocol assignments       RFC 6890
    //   192.0.2.0/24      documentation (TEST-NET-1)      RFC 5737
    //   192.88.99.0/24    6to4 relay anycast (deprecated) RFC 7526
    //   192.168.0.0/16    private use                     RFC 1918
    //   198.18.0.0/15     benchmark testing               RFC 2544
    //   198.51.100.0/24   documentation (TEST-NET-2)      RFC 5737
    //   203.0.113.0/24    documentation (TEST-NET-3)      RFC 5737
    //   224.0.0.0/4       multicast                       RFC 5771
    //   240.0.0.0/4       reserved (former class E) + 255.255.255.255 broadcast
    uint8_t a = (ip_host_order >> 24) & 0xff;
    uint8_t b = (ip_host_order >> 16) & 0xff;
    uint8_t c = (ip_host_order >> 8) & 0xff;
    // Whole /8 blocks
    if (a == 0 || a == 10 || a == 127) return true;
    // /12 RFC 1918
    if (a == 172 && (b & 0xf0) == 16) return true;
    // /16 ranges
    if (a == 169 && b == 254) return true;
    if (a == 192 && b == 168) return true;
    // /10 CGN
    if (a == 100 && (b & 0xc0) == 64) return true;
    // /24 protocol / documentation blocks under 192.x
    if (a == 192 && b == 0 && c == 0) return true;     // IETF protocol assignments
    if (a == 192 && b == 0 && c == 2) return true;     // TEST-NET-1
    if (a == 192 && b == 88 && c == 99) return true;   // 6to4 relay anycast
    // /24 documentation
    if (a == 198 && b == 51 && c == 100) return true;  // TEST-NET-2
    if (a == 203 && b == 0 && c == 113) return true;   // TEST-NET-3
    // /15 benchmark
    if (a == 198 && (b == 18 || b == 19)) return true;
    // Multicast 224.0.0.0/4 and reserved 240.0.0.0/4 (covers 255.255.255.255)
    if (a >= 224) return true;
    return false;
}

bool ipv6_is_private(const struct in6_addr& a) {
    // Documented ranges from RFC 6890 / RFC 4291 / RFC 4193 etc.
    //
    //   ::/128                unspecified
    //   ::1/128               loopback
    //   ::ffff:0:0/96         IPv4-mapped (delegated to IPv4 rules)
    //   100::/64              discard-only address block        RFC 6666
    //   2001:db8::/32         documentation                     RFC 3849
    //   fc00::/7              unique local                       RFC 4193
    //   fe80::/10             link-local                         RFC 4291
    //   fec0::/10             site-local (deprecated, historic)
    //   ff00::/8              multicast                          RFC 4291
    if (IN6_IS_ADDR_LOOPBACK(&a)) return true;
    if (IN6_IS_ADDR_LINKLOCAL(&a)) return true;
    if (IN6_IS_ADDR_SITELOCAL(&a)) return true;
    if (IN6_IS_ADDR_UNSPECIFIED(&a)) return true;
    if (IN6_IS_ADDR_MULTICAST(&a)) return true;
    // fc00::/7 — unique local
    if ((a.s6_addr[0] & 0xfe) == 0xfc) return true;
    // 100::/64 — discard prefix (RFC 6666)
    if (a.s6_addr[0] == 0x01 && a.s6_addr[1] == 0x00 &&
        a.s6_addr[2] == 0x00 && a.s6_addr[3] == 0x00 &&
        a.s6_addr[4] == 0x00 && a.s6_addr[5] == 0x00 &&
        a.s6_addr[6] == 0x00 && a.s6_addr[7] == 0x00) {
        return true;
    }
    // 2001:db8::/32 — documentation prefix (RFC 3849)
    if (a.s6_addr[0] == 0x20 && a.s6_addr[1] == 0x01 &&
        a.s6_addr[2] == 0x0d && a.s6_addr[3] == 0xb8) {
        return true;
    }
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

std::string to_lower(const std::string& s) {
    std::string out;
    out.reserve(s.size());
    for (char c : s) out.push_back(static_cast<char>(std::tolower(static_cast<unsigned char>(c))));
    return out;
}

// Returns true if `host` syntactically looks like an obfuscated IPv4 literal
// that libcurl's URL parser would happily resolve but `inet_pton` rejects in
// strict mode (e.g. `0x7f000001`, `2130706433`, `127.1`). These shapes are
// always unsafe in SSRF context because the canonical form they decode to is
// almost always loopback/private and they exist precisely to evade naive
// string-matching guards.
bool looks_like_obfuscated_ipv4(const std::string& host) {
    if (host.empty()) return false;
    // 0x prefix (hex form): 0x7f000001
    if (host.size() >= 2 && host[0] == '0' && (host[1] == 'x' || host[1] == 'X')) {
        return true;
    }
    // Pure digits (no dot): 2130706433
    bool all_digits = true;
    for (char c : host) {
        if (!std::isdigit(static_cast<unsigned char>(c))) {
            all_digits = false;
            break;
        }
    }
    if (all_digits) return true;
    // Dotted but with fewer than 4 segments: 127.1, 127.0.1
    // (RFC 1123 hostnames can have any number of labels, so we only flag
    // shapes that look numeric in every segment — those are unambiguously
    // attempts at IPv4 short-form, not real hostnames.)
    if (host.find('.') != std::string::npos) {
        bool all_numeric_segments = true;
        size_t segment_count = 1;
        for (char c : host) {
            if (c == '.') {
                ++segment_count;
            } else if (!std::isdigit(static_cast<unsigned char>(c))) {
                all_numeric_segments = false;
                break;
            }
        }
        if (all_numeric_segments && segment_count != 4) return true;
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

bool sockaddr_is_private(const struct sockaddr* sa) {
    if (sa == nullptr) return false;
    if (sa->sa_family == AF_INET) {
        const auto* in4 = reinterpret_cast<const sockaddr_in*>(sa);
        return ipv4_is_private(ntohl(in4->sin_addr.s_addr));
    }
    if (sa->sa_family == AF_INET6) {
        const auto* in6 = reinterpret_cast<const sockaddr_in6*>(sa);
        return ipv6_is_private(in6->sin6_addr);
    }
    return false;
}

bool host_is_private(const std::string& host, std::string* error_out) {
    // Case-insensitive textual aliases.
    const std::string host_lower = to_lower(host);
    if (host_lower.empty() || host_lower == "localhost") {
        if (error_out) *error_out = "host \"" + host + "\" is not allowed (private/loopback)";
        return true;
    }
    // Reject any character outside the RFC 1123 hostname charset
    // (letters, digits, dot, hyphen) or IPv6 literal charset (hex, colon).
    // Stops URL-smuggling shapes like `trusted.com/@127.0.0.1` where the
    // libcurl parser would interpret the `/@127.0.0.1` tail as userinfo /
    // path and connect to 127.0.0.1 anyway. inet_pton() and addrinfo
    // recognise IPv6 explicitly so we don't need to be lenient here.
    for (char c : host) {
        const unsigned char u = static_cast<unsigned char>(c);
        if (!(std::isalnum(u) || c == '-' || c == '.' || c == ':')) {
            if (error_out) *error_out =
                "host \"" + host + "\" contains invalid character; "
                "only RFC 1123 hostnames and IPv6 literals are accepted";
            return true;
        }
    }
    // Reject obfuscated IPv4 shapes that bypass inet_pton but resolve to
    // private addresses in libcurl's own parser (DNS rebinding precursor).
    if (looks_like_obfuscated_ipv4(host)) {
        if (error_out) *error_out = "host \"" + host + "\" looks like a non-canonical IPv4 literal (rejected to defeat SSRF evasion)";
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
