#include "pine/pine.hpp"

#include <sstream>

#if defined(PINE_HAS_STACKTRACE)
#include <stacktrace>
#endif

namespace pine {

namespace {
// Capture the constructing thread's call stack as text. Returns empty
// when the toolchain lacks std::stacktrace linkage (CMake probe sets
// PINE_HAS_STACKTRACE). Mirrors pine-go's runtime.Stack() snapshot taken
// inside the recovery deferred function.
std::string capture_stack() {
#if defined(PINE_HAS_STACKTRACE)
    try {
        auto t = std::stacktrace::current();
        return std::to_string(t);
    } catch (...) {
        return "";  // libbacktrace may fail on stripped binaries
    }
#else
    return "";
#endif
}
}  // namespace

PanicError::PanicError(std::string operator_name, std::string value)
    : Error(format_msg(operator_name, value)),
      operator_(std::move(operator_name)),
      value_(std::move(value)),
      stack_(capture_stack()) {}

std::string PanicError::detailed_error() const {
    // pine-go PanicError.DetailedError() is `pine: panic in operator
    // "X": Y\nstack trace:\n<frames>` — match the same shape so log
    // consumers can keyword-grep across runtimes. When no stack was
    // captured (older toolchain), fall back to just what().
    if (stack_.empty()) return what();
    std::ostringstream os;
    os << what() << "\nstack trace:\n" << stack_;
    return os.str();
}

}  // namespace pine
