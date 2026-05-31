#include <doctest/doctest.h>

#include <string>
#include <vector>

#include "lua/lua_pool.hpp"

using namespace pine;

namespace {

int64_t stat(const std::map<std::string, int64_t>& s, const std::string& key) {
  auto it = s.find(key);
  return it == s.end() ? -1 : it->second;
}

}  // namespace

TEST_CASE("StatePool reuse_count distinguishes pool hits from misses") {
  lua::StatePool pool("function f() return 1 end", "test_op");

  // Construction pre-warms exactly one state: a creation, not a borrow.
  auto s0 = pool.stats_snapshot();
  CHECK(stat(s0, "create_count") == 1);
  CHECK(stat(s0, "reuse_count") == 0);
  CHECK(stat(s0, "borrow_count") == 0);

  // Borrowing then returning (scope exit) repeatedly must reuse the pooled
  // state: reuse_count climbs and no fresh state is created.
  for (int i = 0; i < 5; ++i) {
    auto vm = pool.borrow();
    CHECK(vm != nullptr);
  }
  auto s1 = pool.stats_snapshot();
  CHECK(stat(s1, "reuse_count") > 0);
  // borrow == reuse + on-borrow misses; misses = create_count - 1 (pre-warm).
  CHECK(stat(s1, "borrow_count") == stat(s1, "reuse_count") + (stat(s1, "create_count") - 1));

  // Force a miss by holding two states at once: the second borrow must build a
  // fresh state, bumping create_count.
  int64_t before_create = stat(pool.stats_snapshot(), "create_count");
  {
    auto a = pool.borrow();
    auto b = pool.borrow();
    CHECK(a != nullptr);
    CHECK(b != nullptr);
    CHECK(stat(pool.stats_snapshot(), "create_count") > before_create);
  }

  auto s2 = pool.stats_snapshot();
  CHECK(stat(s2, "borrow_count") == stat(s2, "reuse_count") + (stat(s2, "create_count") - 1));
}
