#include "pine/pine.hpp"

#include <iostream>
#include <string>

int main(int argc, char** argv) {
    std::string config_path;
    std::string format = "dot";
    int collapse = 0;
    for (int i = 1; i < argc; ++i) {
        std::string arg = argv[i];
        if (arg == "-config" && i + 1 < argc) config_path = argv[++i];
        else if (arg == "-format" && i + 1 < argc) format = argv[++i];
        else if (arg == "-collapse" && i + 1 < argc) collapse = std::stoi(argv[++i]);
    }
    if (config_path.empty()) {
        std::cerr << "Usage: pineapple-render-dag -config <path> [-format dot|mermaid] [-collapse N]\n";
        return 1;
    }
    try {
        auto engine = pine::Engine::from_file(config_path);
        std::cout << engine.render_dag(format, collapse);
        return 0;
    } catch (const pine::Error& err) {
        std::cerr << "error rendering DAG: " << err.what() << "\n";
        return 1;
    } catch (const std::exception& err) {
        std::cerr << "error rendering DAG: " << err.what() << "\n";
        return 1;
    }
}
