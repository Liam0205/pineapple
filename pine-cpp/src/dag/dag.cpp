#include "pine/pine.hpp"

#include <algorithm>
#include <deque>
#include <set>

namespace pine {
namespace {

constexpr const char* kRowSet = "_row_set_";

struct FieldTracker {
  int last_mut_writer = -1;
  std::vector<int> additive_writers;
  std::vector<int> active_readers;
};

void add_edge(Graph& graph, int from, int to) {
  if (from == to) {
    return;
  }
  auto& succs = graph.nodes[from].succs;
  if (std::find(succs.begin(), succs.end(), to) != succs.end()) {
    return;
  }
  succs.push_back(to);
  graph.nodes[to].preds.push_back(from);
}

void scan_fields(Graph& graph, const ExpandedSequence& expanded, bool common) {
  std::map<std::string, FieldTracker> trackers;
  for (std::size_t i = 0; i < expanded.sequence.size(); ++i) {
    const auto& op = *graph.nodes[i].config;
    std::vector<std::string> reads = common ? op.metadata.common_read_fields() : op.metadata.item_input;
    std::vector<std::string> writes = common ? op.metadata.common_output : op.metadata.item_output;
    const bool additive = !common && op.additive_writes_row_set;
    if (!common) {
      if (additive) {
        writes.push_back(kRowSet);
      }
      if (op.consumes_row_set) {
        reads.push_back(kRowSet);
      }
      if (!additive && !op.consumes_row_set && (!reads.empty() || !writes.empty())) {
        reads.push_back(kRowSet);
      }
    }
    for (const auto& field : reads) {
      auto& tracker = trackers[field];
      if (tracker.last_mut_writer >= 0) {
        add_edge(graph, tracker.last_mut_writer, static_cast<int>(i));
      }
      for (int writer : tracker.additive_writers) {
        add_edge(graph, writer, static_cast<int>(i));
      }
      tracker.active_readers.push_back(static_cast<int>(i));
    }
    for (const auto& field : writes) {
      auto& tracker = trackers[field];
      if (additive) {
        if (tracker.last_mut_writer >= 0) {
          add_edge(graph, tracker.last_mut_writer, static_cast<int>(i));
        }
        for (int reader : tracker.active_readers) {
          if (reader != static_cast<int>(i)) {
            add_edge(graph, reader, static_cast<int>(i));
          }
        }
        tracker.additive_writers.push_back(static_cast<int>(i));
      } else {
        if (tracker.last_mut_writer >= 0) {
          add_edge(graph, tracker.last_mut_writer, static_cast<int>(i));
        }
        for (int writer : tracker.additive_writers) {
          add_edge(graph, writer, static_cast<int>(i));
        }
        for (int reader : tracker.active_readers) {
          if (reader != static_cast<int>(i)) {
            add_edge(graph, reader, static_cast<int>(i));
          }
        }
        tracker.last_mut_writer = static_cast<int>(i);
        tracker.additive_writers.clear();
        tracker.active_readers.clear();
      }
    }
    if (!common && op.mutates_row_set) {
      auto& tracker = trackers[kRowSet];
      if (tracker.last_mut_writer >= 0) {
        add_edge(graph, tracker.last_mut_writer, static_cast<int>(i));
      }
      for (int writer : tracker.additive_writers) {
        add_edge(graph, writer, static_cast<int>(i));
      }
      for (int reader : tracker.active_readers) {
        if (reader != static_cast<int>(i)) {
          add_edge(graph, reader, static_cast<int>(i));
        }
      }
      tracker.last_mut_writer = static_cast<int>(i);
      tracker.additive_writers.clear();
      tracker.active_readers.clear();
    }
  }
}

bool reachable_without(const Graph& graph, int src, int dst) {
  std::set<int> visited{src};
  std::deque<int> queue;
  for (int next : graph.nodes[src].succs) {
    if (next == dst) {
      continue;
    }
    visited.insert(next);
    queue.push_back(next);
  }
  while (!queue.empty()) {
    int current = queue.front();
    queue.pop_front();
    if (current == dst) {
      return true;
    }
    for (int next : graph.nodes[current].succs) {
      if (!visited.count(next)) {
        visited.insert(next);
        queue.push_back(next);
      }
    }
  }
  return false;
}

void reduce(Graph& graph) {
  std::vector<std::pair<int, int>> kept;
  for (std::size_t u = 0; u < graph.nodes.size(); ++u) {
    for (int v : graph.nodes[u].succs) {
      if (!reachable_without(graph, static_cast<int>(u), v)) {
        kept.emplace_back(static_cast<int>(u), v);
      }
    }
  }
  for (auto& node : graph.nodes) {
    node.preds.clear();
    node.succs.clear();
  }
  for (const auto& [u, v] : kept) {
    add_edge(graph, u, v);
  }
}

void topological_check(const Graph& graph) {
  std::vector<int> indegree;
  for (const auto& node : graph.nodes) {
    indegree.push_back(static_cast<int>(node.preds.size()));
  }
  std::deque<int> queue;
  for (std::size_t i = 0; i < indegree.size(); ++i) {
    if (indegree[i] == 0) {
      queue.push_back(static_cast<int>(i));
    }
  }
  int seen = 0;
  while (!queue.empty()) {
    int current = queue.front();
    queue.pop_front();
    ++seen;
    for (int succ : graph.nodes[current].succs) {
      if (--indegree[succ] == 0) {
        queue.push_back(succ);
      }
    }
  }
  if (seen == static_cast<int>(graph.nodes.size())) {
    return;
  }
  std::vector<std::string> cycle_nodes;
  for (std::size_t i = 0; i < indegree.size(); ++i) {
    if (indegree[i] > 0) {
      cycle_nodes.push_back(graph.nodes[i].name);
    }
  }
  std::string message = "DAG contains a cycle involving operators: [";
  for (std::size_t i = 0; i < cycle_nodes.size(); ++i) {
    if (i) {
      message += ' ';
    }
    message += cycle_nodes[i];
  }
  message += ']';
  throw ConfigError(message);
}

}  // namespace

Graph build_dag(const Config& config, const ExpandedSequence& expanded) {
  Graph graph;
  for (std::size_t i = 0; i < expanded.sequence.size(); ++i) {
    const auto& name = expanded.sequence[i];
    auto op_it = config.operators.find(name);
    if (op_it == config.operators.end()) {
      throw ConfigError("operator \"" + name + "\" not found");
    }
    Node node;
    node.name = name;
    node.subflow = expanded.op_to_subflow.count(name) ? expanded.op_to_subflow.at(name) : "";
    node.config = &op_it->second;
    graph.name_to_index[name] = static_cast<int>(i);
    graph.nodes.push_back(std::move(node));
  }
  scan_fields(graph, expanded, true);
  scan_fields(graph, expanded, false);
  for (std::size_t i = 0; i < expanded.sequence.size(); ++i) {
    const auto& op = *graph.nodes[i].config;
    for (const auto& src : op.sources) {
      if (!graph.name_to_index.count(src)) {
        throw ConfigError("operator \"" + op.name + "\" sources references unknown operator \"" + src + "\"");
      }
      if (graph.name_to_index.at(src) >= static_cast<int>(i)) {
        // Forward-reference checks raise a ValidationError to match
        // pine-go validateSourcesOrder / pine-java Engine.validate /
        // pine-python engine._validate_sources_order. The error text
        // is part of the cross-runtime contract.
        throw ValidationError("operator \"" + op.name + "\": sources references \"" + src +
                              "\" which is declared after the current operator (forward reference)");
      }
      add_edge(graph, graph.name_to_index[src], static_cast<int>(i));
    }
  }
  topological_check(graph);
  reduce(graph);
  return graph;
}

}  // namespace pine
