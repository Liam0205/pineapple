#include "pine/pine.hpp"

#include <fstream>
#include <iostream>
#include <sstream>
#include <string>

namespace {

// Read file contents as a string. Returns true on success.
bool read_file_to_string(const std::string& path, std::string& out) {
    std::ifstream f(path);
    if (!f) return false;
    std::ostringstream oss;
    oss << f.rdbuf();
    out = oss.str();
    return true;
}

}  // namespace

int main(int argc, char** argv) {
    std::string config_path;
    std::string request_path;
    std::string resources_path;
    for (int i = 1; i < argc; ++i) {
        std::string arg = argv[i];
        if (arg == "-config" && i + 1 < argc) config_path = argv[++i];
        else if (arg == "-request" && i + 1 < argc) request_path = argv[++i];
        else if (arg == "-static-resources" && i + 1 < argc) resources_path = argv[++i];
    }
    if (config_path.empty() || request_path.empty()) {
        std::cerr << "Usage: pineapple-run -config <pipeline.json> -request <request.json> [-static-resources <resources.json>]\n";
        return 1;
    }

    // Mirrors pine-go cmd/pineapple-run/main.go: per-phase stderr prefixes.
    std::string config_data;
    if (!read_file_to_string(config_path, config_data)) {
        std::cerr << "error reading config: " << config_path << "\n";
        return 1;
    }

    std::string request_data;
    if (!read_file_to_string(request_path, request_data)) {
        std::cerr << "error reading request: " << request_path << "\n";
        return 1;
    }

    std::unique_ptr<pine::Engine> engine;
    try {
        engine = std::make_unique<pine::Engine>(pine::load_config_from_json(config_data));
    } catch (const std::exception& err) {
        std::cerr << "error creating engine: " << err.what() << "\n";
        return 1;
    }

    pine::Request request;
    try {
        const auto root = pine::parse_json(request_data).as_object();
        if (auto it = root.find("common"); it != root.end()) {
            for (const auto& [key, value] : it->second.as_object()) request.common[key] = value;
        }
        if (auto it = root.find("items"); it != root.end()) {
            for (const auto& item : it->second.as_array()) {
                std::map<std::string, pine::JsonValue> row;
                for (const auto& [key, value] : item.as_object()) row[key] = value;
                request.items.push_back(std::move(row));
            }
        }
    } catch (const std::exception& err) {
        std::cerr << "error parsing request: " << err.what() << "\n";
        return 1;
    }

    std::map<std::string, pine::JsonValue> resources;
    if (!resources_path.empty()) {
        std::string res_data;
        if (!read_file_to_string(resources_path, res_data)) {
            std::cerr << "error reading static resources: " << resources_path << "\n";
            return 1;
        }
        try {
            auto resources_json = pine::parse_json(res_data);
            for (const auto& [key, value] : resources_json.as_object()) resources[key] = value;
        } catch (const std::exception& err) {
            std::cerr << "error parsing static resources: " << err.what() << "\n";
            return 1;
        }
    }

    try {
        std::cout << pine::result_to_json(engine->execute(request, resources));
        return 0;
    } catch (const std::exception& err) {
        std::cerr << "execution error: " << err.what() << "\n";
        return 1;
    }
}
