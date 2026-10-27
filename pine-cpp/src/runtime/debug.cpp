// Request-level debug recorder (issue #219). See include/pine/debug.hpp.

#include "pine/debug.hpp"

#include <any>
#include <utility>

namespace pine::debug {

namespace {

// Private identity key: no other library can collide with its address.
const char kRecorderKey = 0;

}  // namespace

RequestContext Recorder::bind_node(const RequestContext& ctx, NodeMeta meta) const {
  return RequestContext::with_value(ctx, &kRecorderKey, std::any(Recorder(emitter_, std::move(meta))));
}

RequestContext with_emitter(const RequestContext& ctx, Emitter* emitter) {
  if (emitter == nullptr) {
    return ctx;
  }
  return RequestContext::with_value(ctx, &kRecorderKey, std::any(Recorder(emitter, NodeMeta{})));
}

const Recorder* from_context(const RequestContext& ctx) {
  const std::any* v = ctx.value(&kRecorderKey);
  return v == nullptr ? nullptr : std::any_cast<Recorder>(v);
}

}  // namespace pine::debug
