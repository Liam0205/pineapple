#include "pine/pine.hpp"

#include <iostream>
#include <string>

int main(int argc, char** argv) {
    std::string config_path;
    std::string request_path;
    for (int i = 1; i < argc; ++i) {
        std::string arg = argv[i];
        if (arg == "-config" && i + 1 < argc) config_path = argv[++i];
        else if (arg == "-request" && i + 1 < argc) request_path = argv[++i];
    }
    if (config_path.empty() || request_path.empty()) {
        std::cerr << "Usage: pineapple-run -config <pipeline.json> -request <request.json>\n";
        return 1;
    }
    try {
        auto engine = pine::Engine::from_file(config_path);
        auto request = pine::load_request_from_file(request_path);
        std::cout << pine::result_to_json(engine.execute(request));
        return 0;
    } catch (const pine::Error& err) {
        std::cerr << "execution error: " << err.what() << "\n";
        return 1;
    } catch (const std::exception& err) {
        std::cerr << "execution error: " << err.what() << "\n";
        return 1;
    }
}
