#include "server/server.hpp"

#include <cstdlib>
#include <string>

int main(int argc, char** argv) {
    pine::server::ServerConfig cfg;

    for (int i = 1; i < argc; ++i) {
        std::string arg = argv[i];
        if (arg == "-config" && i + 1 < argc) {
            cfg.config_path = argv[++i];
        } else if (arg == "-addr" && i + 1 < argc) {
            cfg.addr = argv[++i];
        }
    }

    if (cfg.config_path.empty()) {
        // Mimic Go: log.Fatal
        fprintf(stderr, "usage: pineapple-server -config <path-to-config.json>\n");
        return 1;
    }

    pine::server::Server server;
    return server.run(cfg);
}
