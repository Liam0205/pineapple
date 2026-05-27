#include "pine/pine.hpp"

#include <fstream>
#include <iostream>
#include <sstream>
#include <string>

int main(int argc, char** argv) {
  std::string config_path;
  std::string format = "dot";
  int collapse = 0;
  for (int i = 1; i < argc; ++i) {
    std::string arg = argv[i];
    if (arg == "-config" && i + 1 < argc) {
      config_path = argv[++i];
    } else if (arg == "-format" && i + 1 < argc) {
      format = argv[++i];
    } else if (arg == "-collapse" && i + 1 < argc) {
      collapse = std::stoi(argv[++i]);
    }
  }
  if (config_path.empty()) {
    std::cerr << "Usage: pineapple-render-dag -config <path> [-format dot|mermaid] [-collapse N]\n";
    return 1;
  }

  // Mirrors pine-go cmd/pineapple-dag/main.go: per-phase stderr prefixes.
  std::ifstream f(config_path);
  if (!f) {
    std::cerr << "error reading config: " << config_path << "\n";
    return 1;
  }
  std::ostringstream oss;
  oss << f.rdbuf();
  std::string config_data = oss.str();

  std::unique_ptr<pine::Engine> engine;
  try {
    engine = std::make_unique<pine::Engine>(pine::load_config_from_json(config_data));
  } catch (const std::exception& err) {
    std::cerr << "error creating engine: " << err.what() << "\n";
    return 1;
  }

  try {
    std::cout << engine->render_dag(format, collapse);
    return 0;
  } catch (const std::exception& err) {
    std::cerr << "error rendering DAG: " << err.what() << "\n";
    return 1;
  }
}
