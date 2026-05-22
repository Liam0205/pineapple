#include "server.hpp"
#include "pine/metrics.hpp"

#include <chrono>

namespace pine {
namespace server {

namespace {

const char* status_bucket(int code) {
    if (code >= 500) return "5xx";
    if (code >= 400) return "4xx";
    if (code >= 300) return "3xx";
    if (code >= 200) return "2xx";
    return "other";
}

}  // namespace

// Builds an HTTP-layer metrics middleware that records request totals and
// duration histograms. Names + buckets match pine-go pkg/server/http_metrics.go.
//
// Usage:
//   cfg.middlewares.push_back(pine::server::http_metrics_middleware(provider));
//
// The provider is captured by value (raw pointer) — callers must keep it
// alive for the lifetime of the server.
Middleware http_metrics_middleware(metrics::Provider* provider) {
    metrics::Counter* requests_total = provider->new_counter(
        {"pine_http_requests_total", "Total HTTP requests.",
         {"method", "path", "status"}});
    metrics::Histogram* request_duration = provider->new_histogram(
        {{"pine_http_request_duration_seconds", "HTTP request duration in seconds.",
          {"method", "path"}},
         {0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}});

    return [requests_total, request_duration](MiddlewareContext& ctx,
                                              const std::function<void()>& next) {
        auto start = std::chrono::steady_clock::now();
        next();
        auto dur = std::chrono::steady_clock::now() - start;
        requests_total->with({ctx.method, ctx.normalized_path, status_bucket(ctx.status)})->inc();
        request_duration->with({ctx.method, ctx.normalized_path})
            ->observe(metrics::duration_seconds(
                std::chrono::duration_cast<std::chrono::nanoseconds>(dur)));
    };
}

}  // namespace server
}  // namespace pine
