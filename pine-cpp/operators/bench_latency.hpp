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
};

class LatencySampler {
 public:
  explicit LatencySampler(LatencyProfile profile)
      : profile_(profile), rng_(std::random_device{}()), dist_(0.0, 1.0), uniform_(0.0, 1.0) {}

  void apply() {
    auto d = sample();
    if (d.count() <= 0) return;
    if (profile_.is_io) {
      std::this_thread::sleep_for(d);
    } else {
      auto deadline = std::chrono::steady_clock::now() + d;
      while (std::chrono::steady_clock::now() < deadline) {
        volatile double acc = 0;
        for (int i = 0; i < 100; ++i) {
          acc += std::sqrt(static_cast<double>(i));
        }
        (void)acc;
      }
    }
  }

 private:
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

  if (profile.p50_mean <= 0 && profile.p99_mean <= 0) return nullptr;

  return std::make_unique<LatencySampler>(profile);
}

}  // namespace pine
