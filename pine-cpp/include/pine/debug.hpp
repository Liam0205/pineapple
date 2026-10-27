#pragma once

// Request-level debug events (issue #219): the C++ counterpart of pine-go
// pkg/debug and pine-java page.liam.pine.debug. It owns no session, buffer,
// serializer, worker or external client. Applications attach an Emitter with
// with_emitter; without one from_context returns nullptr, so build debug-only
// data inside that null check, outside the candidate loop:
//
//   if (const auto* r = pine::debug::from_context(ctx)) {
//     r->emit("candidates", build_debug_view(input));
//   }
//
// The engine binds immutable node metadata before calling each operator. An
// attached recorder preserves normal execution, including data_parallel shards
// and independent DAG nodes: shards of a node share its metadata and may emit
// separate events concurrently; the recorder does not merge them. Intended for
// trusted debug/test requests. It does not turn on the static debug snapshots
// (EngineOptions::debug / JSON `debug`) and does not suppress business writes.
//
// The authoritative implementer's contract is the package doc of
// pine-go/pkg/debug/debug.go; see Emitter below.

#include "pine/pine.hpp"

#include <string>
#include <utility>

namespace pine::debug {

// Identifies the current operator invocation's position in a plan: the
// operator name, its configured type_name, and the name of the pipeline_map
// entry that directly contains it ("" for a top-level node). Session or
// request identity is supplied by the application's Emitter.
struct NodeMeta {
  std::string operator_name;
  std::string type;
  std::string subflow;

  bool operator==(const NodeMeta&) const = default;
};

// Receives application-supplied debug data together with the
// framework-supplied NodeMeta.
//
// - emit may be called concurrently (independent DAG nodes and data_parallel
//   shards emit from different threads).
// - It must consume or copy `data` before returning: input views, nested
//   containers and operator outputs may change afterwards.
// - It must return promptly, without waiting on network I/O, and must handle
//   its own failures. Recorder swallows anything it throws so the business
//   result is unaffected.
// - Capacity limits, supported data types, serialization, delivery and
//   session ownership belong to the implementation, which must outlive every
//   context it is attached to.
class Emitter {
 public:
  virtual ~Emitter() = default;
  virtual void emit(const NodeMeta& meta, const std::string& kind, const Variant& data) = 0;
};

// An immutable, lightweight view of an emitter at a node. Obtained only
// through from_context.
class Recorder {
 public:
  // A child of `ctx` whose recorder reports `meta`. The parent and the shared
  // emitter are not mutated; ctx's values, deadline and cancellation are
  // preserved. The engine calls this before each operator, only when a
  // recorder is attached.
  RequestContext bind_node(const RequestContext& ctx, NodeMeta meta) const;

  // Synchronously forwards `data`; Pineapple neither copies nor encodes it.
  // Anything the emitter throws is swallowed so it cannot reach the business
  // operator. Argument evaluation by the caller is not isolated, so guard
  // debug-only payload construction with the from_context null check.
  void emit(const std::string& kind, const Variant& data) const noexcept {
    try {
      emitter_->emit(meta_, kind, data);
    } catch (...) {
      // A failing emitter must not affect the business result (Go: recover()).
    }
  }

 private:
  friend RequestContext with_emitter(const RequestContext& ctx, Emitter* emitter);
  Recorder(Emitter* emitter, NodeMeta meta) : emitter_(emitter), meta_(std::move(meta)) {
  }

  Emitter* emitter_;
  NodeMeta meta_;
};

// Attaches `emitter` to this request. A null emitter returns ctx unchanged,
// including any recorder it already carries. Emitter / session shutdown
// remains the application's responsibility.
RequestContext with_emitter(const RequestContext& ctx, Emitter* emitter);

// The recorder attached to ctx, or nullptr when there is none. The pointer is
// valid while ctx (or a context derived from it) is alive.
const Recorder* from_context(const RequestContext& ctx);

}  // namespace pine::debug
