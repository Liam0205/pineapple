#include "pine/pine.hpp"

#include <fstream>
#include <iostream>
#include <sstream>
#include <string>

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
    try {
        auto engine = pine::Engine::from_file(config_path);
        auto request = pine::load_request_from_file(request_path);
        if (resources_path.empty()) {
            std::cout << pine::result_to_json(engine.execute(request));
        } else {
            std::ifstream rf(resources_path);
            if (!rf) {
                std::cerr << "execution error: error reading resources: " << resources_path << "\n";
                return 1;
            }
            std::ostringstream oss;
            oss << rf.rdbuf();
            auto resources_json = pine::parse_json(oss.str());
            std::map<std::string, pine::JsonValue> resources;
            for (const auto& [key, value] : resources_json.as_object()) {
                resources[key] = value;
            }
            std::cout << pine::result_to_json(engine.execute(request, resources));
        }
        return 0;
    } catch (const pine::Error& err) {
        std::cerr << "execution error: " << err.what() << "\n";
        return 1;
    } catch (const std::exception& err) {
        std::cerr << "execution error: " << err.what() << "\n";
        return 1;
    }
}
