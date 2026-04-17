// Command pineapple-codegen scans registered Go OperatorSchema and generates
// the Apple Python package with typed operator classes.
//
// Usage:
//
//	go run ./cmd/pineapple-codegen -output apple/generated
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Liam0205/pineapple/internal/registry"

	// Blank-import all operator packages to trigger init() registrations.
	_ "github.com/Liam0205/pineapple/operators"
)

func main() {
	output := flag.String("output", "apple/generated", "Output directory for generated Python files")
	flag.Parse()

	if err := run(*output); err != nil {
		fmt.Fprintf(os.Stderr, "pineapple-codegen: %v\n", err)
		os.Exit(1)
	}
}

func run(outputDir string) error {
	schemas := registry.All()
	if len(schemas) == 0 {
		return fmt.Errorf("no operators registered")
	}

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outputDir, err)
	}

	opTmpl, initTmpl, err := parseTemplates()
	if err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}

	// Generate operators.py
	opPath := filepath.Join(outputDir, "operators.py")
	opFile, err := os.Create(opPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", opPath, err)
	}
	defer opFile.Close()

	if err := opTmpl.Execute(opFile, schemas); err != nil {
		return fmt.Errorf("render operators.py: %w", err)
	}
	fmt.Printf("generated %s (%d operators)\n", opPath, len(schemas))

	// Generate __init__.py
	initPath := filepath.Join(outputDir, "__init__.py")
	initFile, err := os.Create(initPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", initPath, err)
	}
	defer initFile.Close()

	if err := initTmpl.Execute(initFile, schemas); err != nil {
		return fmt.Errorf("render __init__.py: %w", err)
	}
	fmt.Printf("generated %s\n", initPath)

	return nil
}
