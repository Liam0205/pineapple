#pragma once

#include <string>

namespace pine {
namespace http {

// Returns true if `host` looks like a literal loopback / private / link-local
// IPv4 or IPv6 address, or the textual aliases "localhost" / empty string.
// For symbolic hostnames this routine does a best-effort DNS lookup; if the
// lookup fails the host is *not* rejected here (init-time DNS is unreliable
// — the dial-time guard is the real defense).
//
// `error_out` (when non-null) receives a human-readable reason on rejection.
bool host_is_private(const std::string& host, std::string* error_out = nullptr);

// Validates a host for SSRF guard. Returns true when safe to use. On false,
// `error_out` (if non-null) receives the reason.
bool validate_remote_host(const std::string& host, std::string* error_out = nullptr);

// Returns true if the given numeric IP literal (IPv4 dotted-quad or IPv6
// canonical / textual form) is loopback / private / link-local.
bool ip_literal_is_private(const std::string& ip);

}  // namespace http
}  // namespace pine
