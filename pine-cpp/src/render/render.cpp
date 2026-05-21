#include "pine/pine.hpp"

#include <set>
#include <sstream>

namespace pine {
namespace {

std::string sanitize(const std::string& name) {
    std::string out;
    for (char ch : name) out.push_back((std::isalnum(static_cast<unsigned char>(ch)) || ch == '_') ? ch : '_');
    return out;
}

std::string color_for(const std::string& t) {
    if (t == "recall") return "#E8F5E9";
    if (t == "transform") return "#E3F2FD";
    if (t == "filter") return "#FFF3E0";
    if (t == "merge") return "#F3E5F5";
    if (t == "reorder") return "#FFFDE7";
    return "#F5F5F5";
}

std::string collapse_key(const std::string& subflow, int level) {
    if (subflow.empty()) return "";
    std::vector<std::string> parts;
    std::size_t start = 0;
    while (true) {
        auto pos = subflow.find('/', start);
        parts.push_back(subflow.substr(start, pos == std::string::npos ? pos : pos - start));
        if (pos == std::string::npos) break;
        start = pos + 1;
    }
    if (level >= static_cast<int>(parts.size())) return subflow;
    std::string out;
    for (int i = 0; i < level; ++i) {
        if (i) out += '/';
        out += parts[i];
    }
    return out;
}

struct Group {
    std::string key;
    std::string label;
    bool subflow;
};

std::pair<std::vector<Group>, std::vector<std::pair<int, int>>> build_collapsed(const Graph& graph, int level) {
    std::vector<Group> groups;
    std::map<std::string, int> key_to_group;
    std::vector<int> node_to_group(graph.nodes.size());
    for (std::size_t i = 0; i < graph.nodes.size(); ++i) {
        const auto& node = graph.nodes[i];
        const auto key = collapse_key(node.subflow, level);
        if (key.empty()) {
            node_to_group[i] = static_cast<int>(groups.size());
            groups.push_back({"standalone_" + std::to_string(i), node.name, false});
        } else if (!key_to_group.count(key)) {
            key_to_group[key] = static_cast<int>(groups.size());
            node_to_group[i] = static_cast<int>(groups.size());
            groups.push_back({key, key, true});
        } else {
            node_to_group[i] = key_to_group[key];
        }
    }
    std::set<std::pair<int, int>> edges_seen;
    std::vector<std::pair<int, int>> edges;
    for (std::size_t i = 0; i < graph.nodes.size(); ++i) {
        for (int succ : graph.nodes[i].succs) {
            int a = node_to_group[i], b = node_to_group[succ];
            if (a != b && edges_seen.insert({a, b}).second) edges.push_back({a, b});
        }
    }
    return {groups, edges};
}

}  // namespace

std::string render_dot(const Graph& graph) {
    std::ostringstream oss;
    oss << "digraph pipeline {\n    rankdir=TB;\n    node [shape=box, style=filled, fontname=\"Helvetica\"];\n\n";
    for (const auto& node : graph.nodes) {
        oss << "    \"" << node.name << "\" [label=\"" << node.name << "\", fillcolor=\""
            << color_for(node.config ? node.config->operator_type : "transform") << "\"];\n";
    }
    oss << "\n";
    for (const auto& node : graph.nodes) {
        for (int succ : node.succs) {
            oss << "    \"" << node.name << "\" -> \"" << graph.nodes[succ].name << "\";\n";
        }
    }
    oss << "}\n";
    return oss.str();
}

std::string render_mermaid(const Graph& graph) {
    std::ostringstream oss;
    oss << "graph TB\n";
    for (const auto& node : graph.nodes) {
        std::string t = node.config ? node.config->operator_type : "transform";
        oss << "    " << sanitize(node.name) << "[\"" << node.name << "\"]:::" << t << "\n";
    }
    oss << "\n";
    for (const auto& node : graph.nodes) {
        for (int succ : node.succs) {
            oss << "    " << sanitize(node.name) << " --> " << sanitize(graph.nodes[succ].name) << "\n";
        }
    }
    oss << "\n"
        << "    classDef recall fill:#E8F5E9,stroke:#4CAF50\n"
        << "    classDef transform fill:#E3F2FD,stroke:#2196F3\n"
        << "    classDef filter fill:#FFF3E0,stroke:#FF9800\n"
        << "    classDef merge fill:#F3E5F5,stroke:#9C27B0\n"
        << "    classDef reorder fill:#FFFDE7,stroke:#FFC107\n"
        << "    classDef observe fill:#F5F5F5,stroke:#9E9E9E\n";
    return oss.str();
}

std::string render_collapsed_dot(const Graph& graph, int level) {
    auto [groups, edges] = build_collapsed(graph, level);
    std::ostringstream oss;
    oss << "digraph pipeline {\n    rankdir=TB;\n    node [shape=box, style=filled, fontname=\"Helvetica\"];\n\n";
    for (std::size_t i = 0; i < groups.size(); ++i) {
        oss << "    \"g" << i << "\" [label=\"" << groups[i].label << "\", fillcolor=\"" << (groups[i].subflow ? "#BBDEFB" : "#E0E0E0") << "\"];\n";
    }
    oss << "\n";
    for (auto [a, b] : edges) oss << "    \"g" << a << "\" -> \"g" << b << "\";\n";
    oss << "}\n";
    return oss.str();
}

std::string render_collapsed_mermaid(const Graph& graph, int level) {
    auto [groups, edges] = build_collapsed(graph, level);
    std::ostringstream oss;
    oss << "graph TB\n";
    for (std::size_t i = 0; i < groups.size(); ++i) {
        oss << "    g" << i << "[\"" << groups[i].label << "\"]:::"
            << (groups[i].subflow ? "subflow" : "standalone") << "\n";
    }
    oss << "\n";
    for (auto [a, b] : edges) oss << "    g" << a << " --> g" << b << "\n";
    oss << "\n"
        << "    classDef subflow fill:#BBDEFB,stroke:#1976D2\n"
        << "    classDef standalone fill:#E0E0E0,stroke:#616161\n";
    return oss.str();
}

}  // namespace pine
