#include "pine/pine.hpp"

#include <cstring>
#include <fstream>
#include <iostream>

int main(int argc, char* argv[]) {
    std::string schema_path;

    for (int i = 1; i < argc; ++i) {
        if ((std::strcmp(argv[i], "-schema-json") == 0 || std::strcmp(argv[i], "--schema-json") == 0) && i + 1 < argc) {
            schema_path = argv[++i];
        }
    }

    if (schema_path.empty()) {
        std::cerr << "Usage: pineapple-codegen -schema-json <path>\n";
        return 1;
    }

    std::string json = pine::export_schema_json();

    std::ofstream out(schema_path);
    if (!out) {
        std::cerr << "Error: cannot open file for writing: " << schema_path << "\n";
        return 1;
    }
    out << json;
    out.close();

    return 0;
}
