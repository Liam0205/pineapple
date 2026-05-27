#include "pine/error_chain.hpp"
#include "pine/pine.hpp"

#include <doctest/doctest.h>

#include <stdexcept>
#include <string>

namespace {

class FakeRedisError : public std::runtime_error {
 public:
  explicit FakeRedisError(const std::string& msg) : std::runtime_error(msg) {
  }
};

class FakeTimeoutError : public std::runtime_error {
 public:
  explicit FakeTimeoutError(const std::string& msg) : std::runtime_error(msg) {
  }
};

}  // namespace

TEST_CASE("ExecutionError preserves what() byte-exact after nested chain") {
  pine::ExecutionError e("op_x", "redis call failed");
  CHECK(std::string(e.what()) == "pine: execution error in operator \"op_x\": redis call failed");
  CHECK(e.operator_name() == "op_x");
  CHECK(e.inner() == "redis call failed");
}

TEST_CASE("PanicError preserves what() byte-exact after nested chain") {
  pine::PanicError e("op_y", "boom");
  CHECK(std::string(e.what()) == "pine: panic in operator \"op_y\": boom");
  CHECK(e.operator_name() == "op_y");
}

TEST_CASE("std::throw_with_nested + std::rethrow_if_nested round-trips inner cause") {
  bool caught_inner = false;
  try {
    try {
      throw FakeRedisError("key=foo not found");
    } catch (...) {
      std::throw_with_nested(pine::ExecutionError("redis_getter", "lookup failed"));
    }
  } catch (const pine::ExecutionError& outer) {
    try {
      std::rethrow_if_nested(outer);
    } catch (const FakeRedisError& inner) {
      caught_inner = true;
      CHECK(std::string(inner.what()) == "key=foo not found");
    }
  }
  CHECK(caught_inner);
}

TEST_CASE("error_as<T> finds outer exception by type") {
  pine::ExecutionError e("op_x", "boom");
  const pine::ExecutionError* found = pine::error_as<pine::ExecutionError>(e);
  REQUIRE(found != nullptr);
  CHECK(found->operator_name() == "op_x");
}

TEST_CASE("error_as<T> walks nested chain to find inner cause") {
  try {
    try {
      throw FakeRedisError("connection refused");
    } catch (...) {
      std::throw_with_nested(pine::ExecutionError("redis_getter", "fail"));
    }
  } catch (const pine::ExecutionError& e) {
    const FakeRedisError* inner = pine::error_as<FakeRedisError>(e);
    REQUIRE(inner != nullptr);
    CHECK(std::string(inner->what()) == "connection refused");
  }
}

TEST_CASE("error_as<T> returns nullptr when chain has no matching type") {
  try {
    try {
      throw FakeRedisError("connection refused");
    } catch (...) {
      std::throw_with_nested(pine::ExecutionError("redis_getter", "fail"));
    }
  } catch (const pine::ExecutionError& e) {
    CHECK(pine::error_as<FakeTimeoutError>(e) == nullptr);
  }
}

TEST_CASE("error_as<T> handles ExecutionError thrown without nested cause") {
  try {
    throw pine::ExecutionError("op_x", "no inner");
  } catch (const pine::ExecutionError& e) {
    // Should be able to find the outer ExecutionError itself
    REQUIRE(pine::error_as<pine::ExecutionError>(e) != nullptr);
    // No nested cause, FakeRedisError lookup fails cleanly
    CHECK(pine::error_as<FakeRedisError>(e) == nullptr);
  }
}

TEST_CASE("error_is<T> mirrors error_as<T> presence") {
  try {
    try {
      throw FakeTimeoutError("deadline exceeded");
    } catch (...) {
      std::throw_with_nested(pine::ExecutionError("http_op", "remote failed"));
    }
  } catch (const pine::ExecutionError& e) {
    CHECK(pine::error_is<FakeTimeoutError>(e));
    CHECK(pine::error_is<pine::ExecutionError>(e));
    CHECK_FALSE(pine::error_is<FakeRedisError>(e));
  }
}

TEST_CASE("PanicError nested chain also walks correctly") {
  try {
    try {
      throw std::runtime_error("operator died");
    } catch (...) {
      std::throw_with_nested(pine::PanicError("op_x", "operator died"));
    }
  } catch (const pine::PanicError& e) {
    REQUIRE(pine::error_as<std::runtime_error>(e) != nullptr);
  }
}

TEST_CASE("multi-level nested chain unwraps recursively") {
  // Innermost -> middle wrapper -> outermost ExecutionError
  try {
    try {
      try {
        throw FakeRedisError("deepest cause");
      } catch (...) {
        std::throw_with_nested(std::runtime_error("middle layer"));
      }
    } catch (...) {
      std::throw_with_nested(pine::ExecutionError("op", "top-level"));
    }
  } catch (const pine::ExecutionError& outer) {
    // Should be able to recover the deepest FakeRedisError even though
    // it's two levels down.
    const FakeRedisError* deepest = pine::error_as<FakeRedisError>(outer);
    REQUIRE(deepest != nullptr);
    CHECK(std::string(deepest->what()) == "deepest cause");
  }
}
