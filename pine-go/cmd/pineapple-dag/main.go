package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
)

func main() {
	configPath := flag.String("config", "", "path to pipeline JSON config")
	format := flag.String("format", "dot", "output format: dot or mermaid")
	collapse := flag.Int("collapse", 0, "SubFlow collapse level (0 = full graph)")
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: pineapple-dag -config <pipeline.json> [-format dot|mermaid] [-collapse N]")
		os.Exit(1)
	}

	absPath, err := filepath.Abs(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving path: %v\n", err)
		os.Exit(1)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading config: %v\n", err)
		os.Exit(1)
	}

	engine, err := pine.NewEngine(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating engine: %v\n", err)
		os.Exit(1)
	}

	var opts []pine.RenderOption
	if *collapse > 0 {
		opts = append(opts, pine.WithCollapse(*collapse))
	}

	out, err := engine.RenderDAG(*format, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error rendering DAG: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(out)
}
