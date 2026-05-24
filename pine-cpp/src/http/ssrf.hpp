#pragma once

#include <string>

struct sockaddr;

namespace pine {
namespace http {

// Returns true if `host` looks like a literal loopback / private / link-local
// IPv4 or IPv6 address, or the textual aliases "localhost" / empty string.
// For symbolic hostnames this routine does a best-effort DNS lookup; if the
// lookup fails the host is *not* rejected here (init-time DNS is unreliable
// — the dial-time guard is the real defense).
//
// Hostnames that look like obfuscated IPv4 literals (`0x7f000001`,
// `2130706433`, anything with a `0x` prefix, or pure digits) are rejected
// outright — libcurl's URL parser accepts several non-RFC forms that
// `inet_pton` rejects, so init-time validation must guard those shapes.
//
// KNOWN GAP — octal IPv4 literals: forms like `0177.0.0.1` (leading-zero
// dotted-quad) are accepted by getaddrinfo on most glibc systems as
// octal-interpreted IPv4 and resolve to 127.0.0.1. host_is_private's
// numeric-segment heuristic does not flag them because they look like
// any other dotted-quad to inet_pton (which rejects octal) and the
// addrinfo path then has to catch them via the resolved sockaddr.
// This is exactly what sockaddr_is_private at dial time is for: the
// CURLOPT_OPENSOCKETFUNCTION hook intercepts the resolved peer address
// before connect(2), so octal-encoded loopback addresses are rejected
// there even if init-time host_is_private misses them. The dial-time
// guard is therefore the load-bearing defense; host_is_private is a
// fast-fail layer for the shapes we can catch syntactically.
// (P2-25 — inc-6 follow-up)
//
// `error_out` (when non-null) receives a human-readable reason on rejection.
bool host_is_private(const std::string& host, std::string* error_out = nullptr);

// Validates a host for SSRF guard. Returns true when safe to use. On false,
// `error_out` (if non-null) receives the reason.
bool validate_remote_host(const std::string& host, std::string* error_out = nullptr);

// Returns true if the given numeric IP literal (IPv4 dotted-quad or IPv6
// canonical / textual form) is loopback / private / link-local.
bool ip_literal_is_private(const std::string& ip);

// Dial-time guard: returns true if the resolved peer address is private /
// loopback / link-local. Use this from a `CURLOPT_OPENSOCKETFUNCTION`
// callback to defeat DNS rebinding (init-time host validation cannot bind
// libcurl's later DNS resolution to a single IP).
bool sockaddr_is_private(const struct sockaddr* sa);

}  // namespace http
}  // namespace pine
