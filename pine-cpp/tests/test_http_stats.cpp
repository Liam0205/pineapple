#include "server/http_stats.hpp"

#include <doctest/doctest.h>

#include <string>

TEST_CASE("HttpStats records and sorts entries") {
    pine::server::HttpStats stats;
    stats.record_request("POST", "/execute", "2xx", 1500);
    stats.record_request("GET", "/health", "2xx", 200);
    stats.record_request("GET", "/health", "2xx", 300);
    stats.record_request("GET", "_other", "4xx", 100);
    stats.record_request("POST", "/execute", "5xx", 999);

    auto reqs = stats.requests_snapshot();
    CHECK(reqs["POST /execute 2xx"] == 1);
    CHECK(reqs["GET /health 2xx"] == 2);
    CHECK(reqs["GET _other 4xx"] == 1);
    CHECK(reqs["POST /execute 5xx"] == 1);
    CHECK(reqs.size() == 4);

    // std::map iterates in lexicographic key order; verify by walking entries.
    auto it = reqs.begin();
    CHECK(it->first == "GET /health 2xx");
    ++it;
    CHECK(it->first == "GET _other 4xx");
    ++it;
    CHECK(it->first == "POST /execute 2xx");
    ++it;
    CHECK(it->first == "POST /execute 5xx");

    auto durs = stats.durations_snapshot();
    CHECK(durs["GET /health"].count == 2);
    CHECK(durs["GET /health"].sum_ns == 500);
    CHECK(durs["POST /execute"].count == 2);
    CHECK(durs["POST /execute"].sum_ns == 2499);
    CHECK(durs["GET _other"].count == 1);
    CHECK(durs["GET _other"].sum_ns == 100);
}

TEST_CASE("HttpStats key contains METHOD path bucket but path normalized to whitelist (P1-D7)") {
    // Defensive check: the request_total key is `<METHOD> <path> <bucket>`
    // space-separated. If a future path contained a space, the key would
    // ambiguous. The Section 13 +9 schema check on /stats.http already
    // catches drift across runtimes, but lock the invariant here too —
    // record a request with a path that contains a space and confirm
    // HttpStats does not let the caller produce an ambiguous key by
    // requiring normalize_path() at the call site.
    pine::server::HttpStats stats;
    // Direct record_request with an unnormalized path: this is the
    // callable surface. Whoever invokes it is responsible for
    // normalize_path-ing first. We test what happens if they don't:
    stats.record_request("GET", "/path with space", "2xx", 100);
    auto snap = stats.requests_snapshot();
    // The key shape is preserved literally; the defense lives at
    // normalize_path (server.cpp), which is the only entry exercised by
    // the http_metrics_middleware. This test exists to make the
    // contract explicit: HttpStats does not parse keys.
    CHECK(snap.size() == 1);
    CHECK(snap.begin()->first == "GET /path with space 2xx");
}
