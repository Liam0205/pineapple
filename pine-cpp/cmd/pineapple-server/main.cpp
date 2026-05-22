#include "server/server.hpp"

#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <string>
#include <string_view>

namespace {

// parse_duration accepts integer-suffixed Go-style durations: "10s", "30s",
// "1m", "1m30s", "2h"; bare integers are treated as seconds. Returns total
// seconds, or -1 on parse error. Mirrors pine-go flag.Duration's accepted
// subset used by pineapple-server flags. We do NOT support sub-second units
// (ms/us/ns) because the C++ server cannot apply timeouts more granular than
// SO_RCVTIMEO seconds in a meaningful way.
int parse_duration_seconds(std::string_view s) {
    if (s.empty()) return -1;
    int total = 0;
    int cur = 0;
    bool has_digit = false;
    for (std::size_t i = 0; i < s.size(); ++i) {
        char c = s[i];
        if (c >= '0' && c <= '9') {
            cur = cur * 10 + (c - '0');
            has_digit = true;
            continue;
        }
        if (!has_digit) return -1;
        if (c == 'h') {
            total += cur * 3600;
        } else if (c == 'm' && (i + 1 >= s.size() || s[i + 1] != 's')) {
            total += cur * 60;
        } else if (c == 's') {
            total += cur;
        } else {
            return -1;
        }
        cur = 0;
        has_digit = false;
    }
    if (has_digit) total += cur;  // trailing bare integer treated as seconds
    return total;
}

}  // namespace

int main(int argc, char** argv) {
    pine::server::ServerConfig cfg;

    auto take_value = [&](int& i, const char* name) -> const char* {
        if (i + 1 >= argc) {
            fprintf(stderr, "missing value for flag %s\n", name);
            std::exit(2);
        }
        return argv[++i];
    };
    auto take_seconds = [&](int& i, const char* name) -> int {
        const char* raw = take_value(i, name);
        int s = parse_duration_seconds(raw);
        if (s < 0) {
            fprintf(stderr, "invalid duration for %s: %s\n", name, raw);
            std::exit(2);
        }
        return s;
    };

    for (int i = 1; i < argc; ++i) {
        std::string arg = argv[i];
        if (arg == "-config") {
            cfg.config_path = take_value(i, "-config");
        } else if (arg == "-addr") {
            cfg.addr = take_value(i, "-addr");
        } else if (arg == "-read-header-timeout") {
            cfg.read_header_timeout_seconds = take_seconds(i, "-read-header-timeout");
        } else if (arg == "-read-timeout") {
            cfg.read_timeout_seconds = take_seconds(i, "-read-timeout");
        } else if (arg == "-write-timeout") {
            cfg.write_timeout_seconds = take_seconds(i, "-write-timeout");
        } else if (arg == "-idle-timeout") {
            cfg.idle_timeout_seconds = take_seconds(i, "-idle-timeout");
        } else if (arg == "-max-body-size") {
            const char* raw = take_value(i, "-max-body-size");
            cfg.max_request_body_size = std::strtoll(raw, nullptr, 10);
        }
    }

    if (cfg.config_path.empty()) {
        fprintf(stderr,
                "usage: pineapple-server -config <path-to-config.json> "
                "[-addr :8080] [-read-header-timeout 10s] [-read-timeout 30s] "
                "[-write-timeout 60s] [-idle-timeout 120s] [-max-body-size 10485760]\n");
        return 1;
    }

    pine::server::Server server;
    return server.run(cfg);
}
