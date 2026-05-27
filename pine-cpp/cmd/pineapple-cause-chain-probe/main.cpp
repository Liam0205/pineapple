// pineapple-cause-chain-probe: smoke test for ExecutionError cause-chain
// unwrap. Constructs a FakeRedisError, wraps it via std::throw_with_nested
// into pine::ExecutionError, then uses pine::error_as<T>() to recover the
// inner cause. Prints either:
//   PASS:<recovered_inner_msg>
// or
//   FAIL:<reason>
// on stdout. cross-validate Section 15 asserts byte-identical stdout
// across pine-go / pine-java / pine-python / pine-cpp probes.
#include "pine/error_chain.hpp"
#include "pine/pine.hpp"

#include <exception>
#include <iostream>
#include <stdexcept>
#include <string>

namespace {

class FakeRedisError : public std::runtime_error {
 public:
  explicit FakeRedisError(const std::string& key) : std::runtime_error("key=" + key + " not found") {
  }
};

}  // namespace

int main() {
  try {
    try {
      throw FakeRedisError("user:42");
    } catch (...) {
      std::throw_with_nested(pine::ExecutionError("redis_getter", "lookup failed"));
    }
  } catch (const pine::ExecutionError& outer) {
    if (const FakeRedisError* recovered = pine::error_as<FakeRedisError>(outer)) {
      std::cout << "PASS:" << recovered->what() << "\n";
      return 0;
    }
    std::cout << "FAIL:pine::error_as did not recover FakeRedisError from ExecutionError chain\n";
    return 1;
  }
  std::cout << "FAIL:ExecutionError was not caught\n";
  return 1;
}
