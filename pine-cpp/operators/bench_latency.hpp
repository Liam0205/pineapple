#pragma once

#include "pine/pine.hpp"

#include <chrono>
#include <cmath>
#include <cstdint>
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

// Fast RNG: xoshiro256+ (same family as Go's math/rand source)
class FastRng {
 public:
  explicit FastRng(uint64_t seed) {
    s_[0] = splitmix(seed);
    s_[1] = splitmix(s_[0]);
    s_[2] = splitmix(s_[1]);
    s_[3] = splitmix(s_[2]);
  }

  double float64() {
    return static_cast<double>(next() >> 11) * (1.0 / (1ULL << 53));
  }

  double norm_float64() {
    // Box-Muller transform
    double u1 = float64();
    double u2 = float64();
    if (u1 < 1e-300) u1 = 1e-300;
    return std::sqrt(-2.0 * std::log(u1)) * std::cos(2.0 * M_PI * u2);
  }

 private:
  uint64_t s_[4];

  uint64_t next() {
    uint64_t result = s_[0] + s_[3];
    uint64_t t = s_[1] << 17;
    s_[2] ^= s_[0];
    s_[3] ^= s_[1];
    s_[1] ^= s_[2];
    s_[0] ^= s_[3];
    s_[2] ^= t;
    s_[3] = (s_[3] << 45) | (s_[3] >> 19);
    return result;
  }

  static uint64_t splitmix(uint64_t x) {
    x += 0x9e3779b97f4a7c15ULL;
    x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9ULL;
    x = (x ^ (x >> 27)) * 0x94d049bb133111ebULL;
    return x ^ (x >> 31);
  }
};

class LatencySampler {
 public:
  explicit LatencySampler(LatencyProfile profile)
      : profile_(profile), rng_(std::random_device{}()) {}

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
      double a = rng_.float64() * 1000.0 + 1.0;
      double b = rng_.float64() * 1000.0 + 1.0;
      acc += a / b;
      a = rng_.float64() * 1000.0 + 1.0;
      b = rng_.float64() * 1000.0 + 1.0;
      acc -= a / b;
    }
    return acc;
  }

 private:
  double cpu_work(int64_t n) {
    double acc = 1.0;
    for (int64_t i = 0; i < n; ++i) {
      double a = rng_.float64() * 1000.0 + 1.0;
      double b = rng_.float64() * 1000.0 + 1.0;
      acc += a / b;
      a = rng_.float64() * 1000.0 + 1.0;
      b = rng_.float64() * 1000.0 + 1.0;
      acc -= a / b;
    }
    return acc;
  }

  std::chrono::microseconds sample() {
    if (profile_.p50_mean <= 0 && profile_.p99_mean <= 0) {
      return std::chrono::microseconds(0);
    }

    double jitter = rng_.float64();
    double p50 = profile_.p50_mean + jitter * (profile_.p50_max - profile_.p50_mean);
    double p99 = profile_.p99_mean + jitter * (profile_.p99_max - profile_.p99_mean);

    if (p50 <= 0) p50 = 0.001;
    if (p99 <= p50) p99 = p50 * 2;

    double mu = std::log(p50);
    double sigma = (std::log(p99) - mu) / 2.326;
    if (sigma <= 0) sigma = 0.1;

    double s = std::exp(mu + sigma * rng_.norm_float64());

    double cap = p99 * 1.5;
    if (s > cap) s = cap;
    if (s < 0) s = 0;

    return std::chrono::microseconds(static_cast<int64_t>(s * 1000.0));
  }

  LatencyProfile profile_;
  FastRng rng_;
};

inline std::unique_ptr<LatencySampler> parse_bench_profile(const Variant& params) {
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
