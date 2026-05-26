#pragma once

// error_chain.hpp — pine::error_as<T> / pine::error_is<T> helpers for walking
// nested exception chains. Mirrors Go's errors.As / errors.Is and Java's
// Throwable.getCause() + instanceof.
//
// Usage:
//   try {
//       engine.execute(req);
//   } catch (const pine::ExecutionError& e) {
//       if (auto inner = pine::error_as<MyRedisError>(e)) {
//           // recovered the original cause from the nested chain
//           log_redis_failure(inner->key());
//       }
//       if (pine::error_is<MyTimeoutError>(e)) {
//           return retry();
//       }
//   }
//
// Mechanism: pine::ExecutionError and pine::PanicError inherit
// std::nested_exception. When thrown via std::throw_with_nested, the in-flight
// exception is captured as a nested cause. error_as<T>() walks this chain
// recursively, returning the first matching exception pointer, or nullptr
// when no cause along the chain matches T.

#include <exception>
#include <typeinfo>

namespace pine {

namespace detail {

// Step the chain: if `e` is a std::nested_exception with a non-null nested,
// rethrow it and invoke `visitor` on the caught std::exception. Returns
// what visitor returns (or nullptr equivalent if no nested or rethrow fails).
//
// Note: we explicitly check nested_ptr() before invoking rethrow_nested()
// because the standard's rethrow_if_nested() will call rethrow_nested() even
// when nested_ptr() is null, which then calls std::terminate. This matters
// for pine::ExecutionError / pine::PanicError instances that inherit
// std::nested_exception but were thrown directly (not via throw_with_nested),
// leaving nested_ptr() null.
template <typename Visitor>
auto walk_nested(const std::exception& e, Visitor visitor) -> decltype(visitor(e)) {
    const std::nested_exception* nested = dynamic_cast<const std::nested_exception*>(&e);
    if (nested == nullptr) return nullptr;
    if (nested->nested_ptr() == nullptr) return nullptr;
    try {
        nested->rethrow_nested();
    } catch (const std::exception& inner) {
        return visitor(inner);
    } catch (...) {
        // non-std::exception nested cause; we cannot type-match further
        return nullptr;
    }
    return nullptr;
}

}  // namespace detail

// error_as walks the nested_exception chain of `err` and returns a pointer to
// the first exception in the chain that is dynamic_castable to T (including
// `err` itself). Returns nullptr if no match found.
//
// Equivalent to Go's errors.As(err, &target) but typed via dynamic_cast.
//
// Complexity: O(chain_depth) catch + rethrow_nested invocations. The
// recursive call inside walk_nested's visitor descends one level per
// recursive step — total catch count is N, not N² (P2-22 verified the
// reviewer assertion of O(N²) was a misreading of the call structure).
template <typename T>
const T* error_as(const std::exception& err) {
    if (auto self = dynamic_cast<const T*>(&err)) return self;
    return detail::walk_nested(err, [](const std::exception& inner) -> const T* {
        return error_as<T>(inner);
    });
}

// error_is returns true if any exception in the nested chain (including the
// outermost) is dynamic_castable to T. Equivalent to Go's errors.Is(err, T{}).
template <typename T>
bool error_is(const std::exception& err) {
    return error_as<T>(err) != nullptr;
}

}  // namespace pine
