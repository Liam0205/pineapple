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
