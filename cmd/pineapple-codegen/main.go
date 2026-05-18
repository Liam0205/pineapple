// Command pineapple-codegen scans registered Go OperatorSchema and generates
// the Apple Python package with typed operator classes.
//
// Usage:
//
//	go run ./cmd/pineapple-codegen -output apple_generated
//	go run ./cmd/pineapple-codegen -schema-json schema.json
package main

import (
	"flag"
	"fmt"
	"os"

	// Blank-import all operator packages to trigger init() registrations.
	_ "github.com/Liam0205/pineapple/operators"
	"github.com/Liam0205/pineapple/pkg/codegen"
)

func main() {
	output := flag.String("output", "apple_generated", "Output directory for generated Python files")
	docDir := flag.String("doc-dir", "", "Output directory for generated operator docs (empty to skip)")
	opsDir := flag.String("operators-dir", "operators", "Directory containing Go operator source files")
	schemaJSON := flag.String("schema-json", "", "Export operator schema as JSON to this path (skips Python generation)")
	flag.Parse()

	if *schemaJSON != "" {
		if err := codegen.ExportSchemaJSON(*schemaJSON); err != nil {
			fmt.Fprintf(os.Stderr, "pineapple-codegen: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := codegen.Run(codegen.Config{
		OutputDir: *output,
		DocDir:    *docDir,
		OpsDir:    *opsDir,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "pineapple-codegen: %v\n", err)
		os.Exit(1)
	}
}
