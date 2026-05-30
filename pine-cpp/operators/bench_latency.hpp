#pragma once

#include "pine/pine.hpp"

#include <chrono>
#include <cmath>
#include <random>
#include <thread>

namespace pine {

struct LatencyProfile {
  double p50_mean = 0;
  double p50_max = 0;
  double p99_mean = 0;
  double p99_max = 0;
  bool is_io = false;
  int64_t iterations = 0; // calibrated mode: base iteration count for p50_mean
};

class LatencySampler {
 public:
  explicit LatencySampler(LatencyProfile profile)
      : profile_(profile), rng_(std::random_device{}()), dist_(0.0, 1.0), uniform_(0.0, 1.0) {}

  double apply() {
    auto d = sample();
    if (d.count() <= 0) return 0.0;
    if (profile_.is_io) {
      std::this_thread::sleep_for(d);
      return 0.0;
    }
    if (profile_.iterations > 0) {
      // Calibrated mode: scale iterations proportionally to sampled duration
      double target_us = profile_.p50_mean * 1000.0;
      double ratio = static_cast<double>(d.count()) / target_us;
      int64_t n = static_cast<int64_t>(profile_.iterations * ratio);
      if (n < 1) n = 1;
      return cpu_work(n);
    }
    // Time-based mode: compute until timeout
    auto deadline = std::chrono::steady_clock::now() + d;
    double acc = 1.0;
    while (std::chrono::steady_clock::now() < deadline) {
      double a = uniform_(rng_) * 1000.0 + 1.0;
      double b = uniform_(rng_) * 1000.0 + 1.0;
      acc += a / b;
      a = uniform_(rng_) * 1000.0 + 1.0;
      b = uniform_(rng_) * 1000.0 + 1.0;
      acc -= a / b;
    }
    return acc;
  }

 private:
  double cpu_work(int64_t n) {
    double acc = 1.0;
    for (int64_t i = 0; i < n; ++i) {
      double a = uniform_(rng_) * 1000.0 + 1.0;
      double b = uniform_(rng_) * 1000.0 + 1.0;
      acc += a / b;
      a = uniform_(rng_) * 1000.0 + 1.0;
      b = uniform_(rng_) * 1000.0 + 1.0;
      acc -= a / b;
    }
    return acc;
  }

  std::chrono::microseconds sample() {
    if (profile_.p50_mean <= 0 && profile_.p99_mean <= 0) {
      return std::chrono::microseconds(0);
    }

    double jitter = uniform_(rng_);
    double p50 = profile_.p50_mean + jitter * (profile_.p50_max - profile_.p50_mean);
    double p99 = profile_.p99_mean + jitter * (profile_.p99_max - profile_.p99_mean);

    if (p50 <= 0) p50 = 0.001;
    if (p99 <= p50) p99 = p50 * 2;

    double mu = std::log(p50);
    double sigma = (std::log(p99) - mu) / 2.326;
    if (sigma <= 0) sigma = 0.1;

    double s = std::exp(mu + sigma * dist_(rng_));

    double cap = p99 * 1.5;
    if (s > cap) s = cap;
    if (s < 0) s = 0;

    return std::chrono::microseconds(static_cast<int64_t>(s * 1000.0));
  }

  LatencyProfile profile_;
  std::mt19937 rng_;
  std::normal_distribution<double> dist_;
  std::uniform_real_distribution<double> uniform_;
};

inline std::unique_ptr<LatencySampler> parse_bench_profile(const JsonValue& params) {
  if (!params.is_object()) return nullptr;
  auto it = params.as_object().find("bench_profile");
  if (it == params.as_object().end() || it->second.is_null()) return nullptr;
  if (!it->second.is_object()) return nullptr;

  const auto& m = it->second.as_object();
  LatencyProfile profile;

  auto p50_it = m.find("p50");
  if (p50_it != m.end() && p50_it->second.is_array()) {
    const auto& arr = p50_it->second.as_array();
    if (arr.size() >= 2) {
      if (arr[0].is_number()) profile.p50_mean = arr[0].as_number();
      if (arr[1].is_number()) profile.p50_max = arr[1].as_number();
    }
  }

  auto p99_it = m.find("p99");
  if (p99_it != m.end() && p99_it->second.is_array()) {
    const auto& arr = p99_it->second.as_array();
    if (arr.size() >= 2) {
      if (arr[0].is_number()) profile.p99_mean = arr[0].as_number();
      if (arr[1].is_number()) profile.p99_max = arr[1].as_number();
    }
  }

  auto type_it = m.find("type");
  if (type_it != m.end() && type_it->second.is_string()) {
    profile.is_io = (type_it->second.as_string() == "io");
  }

  auto iter_it = m.find("iterations");
  if (iter_it != m.end() && iter_it->second.is_number()) {
    profile.iterations = static_cast<int64_t>(iter_it->second.as_number());
  }

  if (profile.p50_mean <= 0 && profile.p99_mean <= 0) return nullptr;

  return std::make_unique<LatencySampler>(profile);
}

}  // namespace pine
