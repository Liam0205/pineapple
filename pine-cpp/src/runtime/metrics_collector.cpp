#include "pine/metrics_collector.hpp"

#include <cmath>

#include "config/json_writer.hpp"

namespace pine {
namespace metrics {

namespace {

std::string label_key(const std::vector<std::string>& values) {
  std::string out;
  for (std::size_t i = 0; i < values.size(); ++i) {
    if (i > 0) {
      out += " ";
    }
    out += values[i];
  }
  return out;
}

std::string json_escape_str(const std::string& s) {
  std::string out;
  out.reserve(s.size() + 2);
  for (char c : s) {
    switch (c) {
      case '"':
        out += "\\\"";
        break;
      case '\\':
        out += "\\\\";
        break;
      case '\n':
        out += "\\n";
        break;
      case '\r':
        out += "\\r";
        break;
      case '\t':
        out += "\\t";
        break;
      default:
        out += c;
        break;
    }
  }
  return out;
}

}  // namespace

// ─── Collector handle types ──────────────────────────────────────────────────

class Collector::CollectorCounter : public Counter {
 public:
  CollectorCounter(Collector* c, std::string name, std::string key)
      : c_(c), name_(std::move(name)), key_(std::move(key)) {
  }
  Counter* with(const std::vector<std::string>& label_values) override {
    return c_->intern_counter(name_, label_key(label_values));
  }
  void inc() override {
    c_->inc_counter(name_, key_);
  }

 private:
  Collector* c_;
  std::string name_;
  std::string key_;
};

class Collector::CollectorGauge : public Gauge {
 public:
  CollectorGauge(Collector* c, std::string name, std::string key)
      : c_(c), name_(std::move(name)), key_(std::move(key)) {
  }
  Gauge* with(const std::vector<std::string>& label_values) override {
    return c_->intern_gauge(name_, label_key(label_values));
  }
  void set(double value) override {
    c_->set_gauge(name_, key_, value);
  }
  void add(double delta) override {
    c_->add_gauge(name_, key_, delta);
  }

 private:
  Collector* c_;
  std::string name_;
  std::string key_;
};

class Collector::CollectorHistogram : public Histogram {
 public:
  CollectorHistogram(Collector* c, std::string name, std::string key)
      : c_(c), name_(std::move(name)), key_(std::move(key)) {
  }
  Histogram* with(const std::vector<std::string>& label_values) override {
    return c_->intern_histogram(name_, label_key(label_values));
  }
  void observe(double value) override {
    c_->observe_hist(name_, key_, value);
  }

 private:
  Collector* c_;
  std::string name_;
  std::string key_;
};

// ─── Collector ───────────────────────────────────────────────────────────────

Collector::Collector() = default;
Collector::~Collector() = default;

Counter* Collector::new_counter(const MetricOpts& opts) {
  std::lock_guard<std::mutex> lock(mu_);
  counters_[opts.name];  // register name so it appears even when unobserved
  counter_objs_.push_back(std::make_unique<CollectorCounter>(this, opts.name, ""));
  return counter_objs_.back().get();
}

Gauge* Collector::new_gauge(const MetricOpts& opts) {
  std::lock_guard<std::mutex> lock(mu_);
  gauges_[opts.name];
  gauge_objs_.push_back(std::make_unique<CollectorGauge>(this, opts.name, ""));
  return gauge_objs_.back().get();
}

Histogram* Collector::new_histogram(const HistogramOpts& opts) {
  std::lock_guard<std::mutex> lock(mu_);
  histograms_[opts.opts.name];
  hist_objs_.push_back(std::make_unique<CollectorHistogram>(this, opts.opts.name, ""));
  return hist_objs_.back().get();
}

Counter* Collector::intern_counter(const std::string& name, const std::string& key) {
  std::lock_guard<std::mutex> lock(mu_);
  counter_objs_.push_back(std::make_unique<CollectorCounter>(this, name, key));
  return counter_objs_.back().get();
}

Gauge* Collector::intern_gauge(const std::string& name, const std::string& key) {
  std::lock_guard<std::mutex> lock(mu_);
  gauge_objs_.push_back(std::make_unique<CollectorGauge>(this, name, key));
  return gauge_objs_.back().get();
}

Histogram* Collector::intern_histogram(const std::string& name, const std::string& key) {
  std::lock_guard<std::mutex> lock(mu_);
  hist_objs_.push_back(std::make_unique<CollectorHistogram>(this, name, key));
  return hist_objs_.back().get();
}

void Collector::inc_counter(const std::string& name, const std::string& key) {
  std::lock_guard<std::mutex> lock(mu_);
  counters_[name][key]++;
}

void Collector::set_gauge(const std::string& name, const std::string& key, double value) {
  std::lock_guard<std::mutex> lock(mu_);
  gauges_[name][key] = value;
}

void Collector::add_gauge(const std::string& name, const std::string& key, double delta) {
  std::lock_guard<std::mutex> lock(mu_);
  gauges_[name][key] += delta;
}

void Collector::observe_hist(const std::string& name, const std::string& key, double value) {
  std::lock_guard<std::mutex> lock(mu_);
  HistCell& cell = histograms_[name][key];
  cell.count++;
  // Accumulate seconds as integer nanoseconds, matching the http subtree's
  // sum_ns so /stats stays float-free and byte-exact across runtimes.
  cell.sum_ns += static_cast<int64_t>(std::llround(value * 1e9));
}

std::string Collector::to_json() const {
  std::lock_guard<std::mutex> lock(mu_);
  // Merge all three metric kinds into one name-sorted object. A metric name is
  // only ever one kind, so there is no key collision; std::map gives the
  // lexicographic ordering that matches Go's encoding/json for map[string]any.
  std::map<std::string, std::string> by_name;

  for (const auto& [name, cells] : counters_) {
    std::string inner = "{";
    bool first = true;
    for (const auto& [k, v] : cells) {
      if (!first) {
        inner += ",";
      }
      first = false;
      inner += "\"" + json_escape_str(k) + "\":" + go_format_json_number(v);
    }
    inner += "}";
    by_name[name] = inner;
  }
  for (const auto& [name, cells] : gauges_) {
    std::string inner = "{";
    bool first = true;
    for (const auto& [k, v] : cells) {
      if (!first) {
        inner += ",";
      }
      first = false;
      inner += "\"" + json_escape_str(k) + "\":" + go_format_json_number(v);
    }
    inner += "}";
    by_name[name] = inner;
  }
  for (const auto& [name, cells] : histograms_) {
    std::string inner = "{";
    bool first = true;
    for (const auto& [k, cell] : cells) {
      if (!first) {
        inner += ",";
      }
      first = false;
      inner += "\"" + json_escape_str(k) + "\":{\"count\":" + std::to_string(cell.count) +
               ",\"sum_ns\":" + std::to_string(cell.sum_ns) + "}";
    }
    inner += "}";
    by_name[name] = inner;
  }

  std::string out = "{";
  bool first = true;
  for (const auto& [name, inner] : by_name) {
    if (!first) {
      out += ",";
    }
    first = false;
    out += "\"" + json_escape_str(name) + "\":" + inner;
  }
  out += "}";
  return out;
}

// ─── TeeProvider handle types ────────────────────────────────────────────────

class TeeProvider::TeeCounter : public Counter {
 public:
  TeeCounter(TeeProvider* p, std::vector<Counter*> children) : p_(p), children_(std::move(children)) {
  }
  Counter* with(const std::vector<std::string>& label_values) override {
    std::vector<Counter*> cs;
    cs.reserve(children_.size());
    for (auto* c : children_) {
      cs.push_back(c->with(label_values));
    }
    return p_->intern_counter(std::move(cs));
  }
  void inc() override {
    for (auto* c : children_) {
      c->inc();
    }
  }

 private:
  TeeProvider* p_;
  std::vector<Counter*> children_;
};

class TeeProvider::TeeGauge : public Gauge {
 public:
  TeeGauge(TeeProvider* p, std::vector<Gauge*> children) : p_(p), children_(std::move(children)) {
  }
  Gauge* with(const std::vector<std::string>& label_values) override {
    std::vector<Gauge*> gs;
    gs.reserve(children_.size());
    for (auto* g : children_) {
      gs.push_back(g->with(label_values));
    }
    return p_->intern_gauge(std::move(gs));
  }
  void set(double value) override {
    for (auto* g : children_) {
      g->set(value);
    }
  }
  void add(double delta) override {
    for (auto* g : children_) {
      g->add(delta);
    }
  }

 private:
  TeeProvider* p_;
  std::vector<Gauge*> children_;
};

class TeeProvider::TeeHistogram : public Histogram {
 public:
  TeeHistogram(TeeProvider* p, std::vector<Histogram*> children) : p_(p), children_(std::move(children)) {
  }
  Histogram* with(const std::vector<std::string>& label_values) override {
    std::vector<Histogram*> hs;
    hs.reserve(children_.size());
    for (auto* h : children_) {
      hs.push_back(h->with(label_values));
    }
    return p_->intern_histogram(std::move(hs));
  }
  void observe(double value) override {
    for (auto* h : children_) {
      h->observe(value);
    }
  }

 private:
  TeeProvider* p_;
  std::vector<Histogram*> children_;
};

// ─── TeeProvider ─────────────────────────────────────────────────────────────

TeeProvider::TeeProvider(std::vector<Provider*> providers) : providers_(std::move(providers)) {
}
TeeProvider::~TeeProvider() = default;

Counter* TeeProvider::new_counter(const MetricOpts& opts) {
  std::vector<Counter*> cs;
  cs.reserve(providers_.size());
  for (auto* p : providers_) {
    cs.push_back(p->new_counter(opts));
  }
  return intern_counter(std::move(cs));
}

Gauge* TeeProvider::new_gauge(const MetricOpts& opts) {
  std::vector<Gauge*> gs;
  gs.reserve(providers_.size());
  for (auto* p : providers_) {
    gs.push_back(p->new_gauge(opts));
  }
  return intern_gauge(std::move(gs));
}

Histogram* TeeProvider::new_histogram(const HistogramOpts& opts) {
  std::vector<Histogram*> hs;
  hs.reserve(providers_.size());
  for (auto* p : providers_) {
    hs.push_back(p->new_histogram(opts));
  }
  return intern_histogram(std::move(hs));
}

Counter* TeeProvider::intern_counter(std::vector<Counter*> children) {
  std::lock_guard<std::mutex> lock(mu_);
  counter_objs_.push_back(std::make_unique<TeeCounter>(this, std::move(children)));
  return counter_objs_.back().get();
}

Gauge* TeeProvider::intern_gauge(std::vector<Gauge*> children) {
  std::lock_guard<std::mutex> lock(mu_);
  gauge_objs_.push_back(std::make_unique<TeeGauge>(this, std::move(children)));
  return gauge_objs_.back().get();
}

Histogram* TeeProvider::intern_histogram(std::vector<Histogram*> children) {
  std::lock_guard<std::mutex> lock(mu_);
  hist_objs_.push_back(std::make_unique<TeeHistogram>(this, std::move(children)));
  return hist_objs_.back().get();
}

}  // namespace metrics
}  // namespace pine
