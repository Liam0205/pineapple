// RequestContext: request-scoped cancellation, deadline, values and resources
// (issue #217). See the class comment in include/pine/pine.hpp.

#include "pine/pine.hpp"

#include <condition_variable>
#include <cstdint>
#include <map>
#include <mutex>
#include <thread>
#include <unordered_map>
#include <utility>
#include <vector>

namespace pine {
namespace detail {

namespace {

constexpr int kNotDone = 0;
constexpr int kCanceled = 1;
constexpr int kDeadlineExceeded = 2;

}  // namespace

// One link in the value / resource chain. Cancel contexts add no node: they
// share their parent's chain and only swap the signal.
struct ContextNode {
  std::shared_ptr<const ContextNode> parent;
  const void* key = nullptr;  // non-null on a with_value node
  std::any value;
  const std::map<std::string, Variant>* resources = nullptr;
  const ResourceProvider* provider = nullptr;
};

// Cancellation state of one cancelable context. A child links to its parent
// with a std::stop_callback, so a parent's cancel reaches every descendant.
// The link is dropped when the child is cancelled on its own or destroyed,
// so a long-lived parent (a server's root context) does not accumulate one
// callback per finished request.
class ContextSignal {
 public:
  explicit ContextSignal(std::optional<RequestContext::Clock::time_point> deadline) : deadline_(deadline) {
  }
  ~ContextSignal();
  ContextSignal(const ContextSignal&) = delete;
  ContextSignal& operator=(const ContextSignal&) = delete;

  void link(const std::shared_ptr<ContextSignal>& parent);
  void arm_deadline(const std::shared_ptr<ContextSignal>& self);
  // cause is kCanceled or kDeadlineExceeded. The first call wins.
  void cancel(int cause, bool from_parent);

  int cause() const {
    return cause_.load(std::memory_order_acquire);
  }
  std::stop_token token() const {
    return src_.get_token();
  }
  const std::optional<RequestContext::Clock::time_point>& deadline() const {
    return deadline_;
  }

 private:
  struct ParentFired {
    ContextSignal* self;
    std::shared_ptr<ContextSignal> parent;
    void operator()() noexcept {
      self->cancel(parent->cause(), /*from_parent=*/true);
    }
  };
  using Link = std::stop_callback<ParentFired>;

  void release_link();

  const std::optional<RequestContext::Clock::time_point> deadline_;
  std::stop_source src_;
  std::atomic<int> cause_{kNotDone};
  std::atomic<uint64_t> timer_id_{0};
  std::mutex link_mu_;
  std::unique_ptr<Link> parent_link_;
};

namespace {

// DeadlineTimer is the process-wide thread that cancels contexts whose
// deadline passed. One thread serves every context; an entry is removed as
// soon as its context is done or destroyed, so a request that finishes before
// its deadline (the common case) leaves nothing behind. Deliberately leaked:
// contexts may be destroyed during static destruction, after a function-local
// static timer would already be gone.
class DeadlineTimer {
 public:
  using Clock = RequestContext::Clock;

  static DeadlineTimer& instance() {
    static DeadlineTimer* timer = new DeadlineTimer();
    return *timer;
  }

  uint64_t add(Clock::time_point when, std::weak_ptr<ContextSignal> signal) {
    std::lock_guard<std::mutex> lk(mu_);
    uint64_t id = next_id_++;
    auto it = by_time_.emplace(when, Entry{id, std::move(signal)});
    by_id_.emplace(id, it);
    if (it == by_time_.begin()) {
      cv_.notify_one();
    }
    return id;
  }

  void remove(uint64_t id) {
    std::lock_guard<std::mutex> lk(mu_);
    auto it = by_id_.find(id);
    if (it != by_id_.end()) {
      by_time_.erase(it->second);
      by_id_.erase(it);
    }
  }

 private:
  struct Entry {
    uint64_t id;
    std::weak_ptr<ContextSignal> signal;
  };
  using ByTime = std::multimap<Clock::time_point, Entry>;

  DeadlineTimer() {
    std::thread([this] { loop(); }).detach();
  }

  void loop() {
    std::unique_lock<std::mutex> lk(mu_);
    std::vector<std::weak_ptr<ContextSignal>> due;
    for (;;) {
      if (by_time_.empty()) {
        cv_.wait(lk);
        continue;
      }
      auto now = Clock::now();
      if (by_time_.begin()->first > now) {
        cv_.wait_until(lk, by_time_.begin()->first);
        continue;
      }
      while (!by_time_.empty() && by_time_.begin()->first <= now) {
        auto it = by_time_.begin();
        due.push_back(std::move(it->second.signal));
        by_id_.erase(it->second.id);
        by_time_.erase(it);
      }
      // Cancel outside the lock: cancel() runs the descendants' callbacks,
      // and a signal destroyed here (last owner gone) calls remove().
      lk.unlock();
      for (auto& w : due) {
        if (auto s = w.lock()) {
          s->cancel(kDeadlineExceeded, /*from_parent=*/false);
        }
      }
      due.clear();
      lk.lock();
    }
  }

  std::mutex mu_;
  std::condition_variable cv_;
  ByTime by_time_;
  std::unordered_map<uint64_t, ByTime::iterator> by_id_;
  uint64_t next_id_ = 1;
};

}  // namespace

ContextSignal::~ContextSignal() {
  // Unregister from the parent first, while every member is still alive: a
  // parent cancelling concurrently may be inside ParentFired, and ~Link waits
  // for it to return.
  release_link();
  if (uint64_t id = timer_id_.load(std::memory_order_acquire)) {
    DeadlineTimer::instance().remove(id);
  }
}

void ContextSignal::link(const std::shared_ptr<ContextSignal>& parent) {
  // Runs ParentFired immediately, on this thread, if the parent is already
  // done. ParentFired never touches link_mu_ / parent_link_, so constructing
  // under the lock is safe.
  auto link = std::make_unique<Link>(parent->token(), ParentFired{this, parent});
  std::lock_guard<std::mutex> lk(link_mu_);
  parent_link_ = std::move(link);
}

void ContextSignal::arm_deadline(const std::shared_ptr<ContextSignal>& self) {
  if (cause() != kNotDone) {
    return;
  }
  if (*deadline_ <= RequestContext::Clock::now()) {
    cancel(kDeadlineExceeded, /*from_parent=*/false);
    return;
  }
  timer_id_.store(DeadlineTimer::instance().add(*deadline_, self), std::memory_order_release);
  // A cancel that raced the add above saw timer_id_ == 0; drop the entry now.
  if (cause() != kNotDone) {
    DeadlineTimer::instance().remove(timer_id_.load(std::memory_order_acquire));
  }
}

void ContextSignal::cancel(int cause, bool from_parent) {
  int expected = kNotDone;
  if (!cause_.compare_exchange_strong(expected, cause, std::memory_order_acq_rel)) {
    return;
  }
  // cause_ is published before the stop request, so anyone woken through
  // the token reads the right err().
  src_.request_stop();
  if (uint64_t id = timer_id_.load(std::memory_order_acquire)) {
    DeadlineTimer::instance().remove(id);
  }
  // From the parent's callback the link is the callback being run: it must
  // not be destroyed from inside itself. The parent's stop request has
  // already deregistered it, and ~ContextSignal frees it.
  if (!from_parent) {
    release_link();
  }
}

void ContextSignal::release_link() {
  std::unique_ptr<Link> link;
  {
    std::lock_guard<std::mutex> lk(link_mu_);
    link.swap(parent_link_);
  }
  // Destroyed outside link_mu_: ~Link may wait for a concurrent ParentFired.
}

}  // namespace detail

namespace {

std::shared_ptr<detail::ContextSignal> new_child_signal(
    const std::shared_ptr<detail::ContextSignal>& parent,
    std::optional<RequestContext::Clock::time_point> deadline) {
  auto s = std::make_shared<detail::ContextSignal>(deadline);
  if (parent) {
    s->link(parent);
  }
  return s;
}

}  // namespace

RequestContext::Cancelable RequestContext::with_cancel(const RequestContext& parent) {
  std::optional<Clock::time_point> inherited;
  if (parent.signal_) {
    inherited = parent.signal_->deadline();
  }
  return Cancelable(parent.node_, new_child_signal(parent.signal_, inherited));
}

RequestContext::Cancelable RequestContext::with_deadline(const RequestContext& parent,
                                                         Clock::time_point deadline) {
  if (parent.signal_ && parent.signal_->deadline() && *parent.signal_->deadline() <= deadline) {
    return with_cancel(parent);
  }
  auto s = new_child_signal(parent.signal_, deadline);
  s->arm_deadline(s);
  return Cancelable(parent.node_, std::move(s));
}

RequestContext::Cancelable RequestContext::with_timeout(const RequestContext& parent,
                                                        Clock::duration timeout) {
  return with_deadline(parent, Clock::now() + timeout);
}

RequestContext RequestContext::with_value(const RequestContext& parent, const void* key, std::any value) {
  if (key == nullptr) {
    throw std::invalid_argument("RequestContext::with_value: key must not be null");
  }
  auto n = std::make_shared<detail::ContextNode>();
  n->parent = parent.node_;
  n->key = key;
  n->value = std::move(value);
  return RequestContext(std::move(n), parent.signal_);
}

RequestContext RequestContext::with_resources(const RequestContext& parent,
                                              const std::map<std::string, Variant>* resources) {
  auto n = std::make_shared<detail::ContextNode>();
  n->parent = parent.node_;
  n->resources = resources;
  return RequestContext(std::move(n), parent.signal_);
}

RequestContext RequestContext::with_resource_provider(const RequestContext& parent,
                                                      const ResourceProvider* provider) {
  auto n = std::make_shared<detail::ContextNode>();
  n->parent = parent.node_;
  n->provider = provider;
  return RequestContext(std::move(n), parent.signal_);
}

bool RequestContext::cancelled() const {
  return signal_ && signal_->cause() != detail::kNotDone;
}

std::optional<ContextError> RequestContext::err() const {
  if (!signal_) {
    return std::nullopt;
  }
  switch (signal_->cause()) {
    case detail::kCanceled:
      return ContextError(false);
    case detail::kDeadlineExceeded:
      return ContextError(true);
    default:
      return std::nullopt;
  }
}

void RequestContext::throw_if_cancelled() const {
  if (auto e = err()) {
    throw *e;
  }
}

std::stop_token RequestContext::stop_token() const {
  return signal_ ? signal_->token() : std::stop_token{};
}

std::optional<RequestContext::Clock::time_point> RequestContext::deadline() const {
  return signal_ ? signal_->deadline() : std::nullopt;
}

const std::any* RequestContext::value(const void* key) const {
  for (const detail::ContextNode* n = node_.get(); n != nullptr; n = n->parent.get()) {
    if (n->key != nullptr && n->key == key) {
      return &n->value;
    }
  }
  return nullptr;
}

const std::map<std::string, Variant>* RequestContext::resources() const {
  for (const detail::ContextNode* n = node_.get(); n != nullptr; n = n->parent.get()) {
    if (n->resources != nullptr) {
      return n->resources;
    }
  }
  return nullptr;
}

const ResourceProvider* RequestContext::resource_provider() const {
  for (const detail::ContextNode* n = node_.get(); n != nullptr; n = n->parent.get()) {
    if (n->provider != nullptr) {
      return n->provider;
    }
  }
  return nullptr;
}

bool RequestContext::wait_for(Clock::duration timeout) const {
  if (!signal_) {
    std::this_thread::sleep_for(timeout);
    return false;
  }
  std::mutex mu;
  std::condition_variable_any cv;
  std::unique_lock<std::mutex> lk(mu);
  cv.wait_for(lk, signal_->token(), timeout, [] { return false; });
  return cancelled();
}

RequestContext::Cancelable& RequestContext::Cancelable::operator=(Cancelable&& other) noexcept {
  if (this != &other) {
    cancel();
    node_ = std::move(other.node_);
    signal_ = std::move(other.signal_);
  }
  return *this;
}

RequestContext::Cancelable::~Cancelable() {
  cancel();
}

void RequestContext::Cancelable::cancel() const {
  if (signal_) {
    signal_->cancel(detail::kCanceled, /*from_parent=*/false);
  }
}

}  // namespace pine
