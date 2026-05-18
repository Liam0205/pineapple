package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
)

func main() {
	configPath := flag.String("config", "", "path to pipeline JSON config")
	requestPath := flag.String("request", "", "path to request JSON (with common and items fields)")
	flag.Parse()

	if *configPath == "" || *requestPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: pineapple-run -config <pipeline.json> -request <request.json>")
		os.Exit(1)
	}

	configData, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading config: %v\n", err)
		os.Exit(1)
	}

	reqData, err := os.ReadFile(*requestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading request: %v\n", err)
		os.Exit(1)
	}

	engine, err := pine.NewEngine(configData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating engine: %v\n", err)
		os.Exit(1)
	}

	var req pine.Request
	if err := json.Unmarshal(reqData, &req); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing request: %v\n", err)
		os.Exit(1)
	}

	result, err := engine.Execute(context.Background(), &req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "execution error: %v\n", err)
		os.Exit(1)
	}

	output := map[string]any{
		"common": result.Common,
		"items":  result.Items,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding result: %v\n", err)
		os.Exit(1)
	}
}
