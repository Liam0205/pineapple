package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

func main() {
	configPath := flag.String("config", "", "path to pipeline JSON config")
	requestPath := flag.String("request", "", "path to request JSON (with common and items fields)")
	resourcesPath := flag.String("static-resources", "", "path to static resources JSON (optional)")
	flag.Parse()

	if *configPath == "" || *requestPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: pineapple-run -config <pipeline.json> -request <request.json> [-static-resources <resources.json>]")
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

	ctx := context.Background()

	// Register standard 'static' fetchors to registry dynamically in Go's pineapple-run just like C++ does
	// to allow CLI runtimes loading error test payloads or static pipelines with local candidates smoothly.
	resource.Register(pine.ResourceSchema{
		Name:            "static",
		Description:     "static test resource",
		DefaultInterval: 3600,
	}, func(params map[string]any) (resource.Fetcher, error) {
		val := params["value"]
		return func(ctx context.Context) (any, error) {
			return val, nil
		}, nil
	})

	rm := resource.NewManager()
	if err := rm.LoadFromRootConfig(configData); err == nil {
		if err := rm.Start(ctx); err == nil {
			ctx = resource.WithResources(ctx, rm)
			defer rm.Stop()
		}
	}

	if *resourcesPath != "" {
		resData, err := os.ReadFile(*resourcesPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading static resources: %v\n", err)
			os.Exit(1)
		}
		var resources map[string]any
		if err := json.Unmarshal(resData, &resources); err != nil {
			fmt.Fprintf(os.Stderr, "error parsing static resources: %v\n", err)
			os.Exit(1)
		}
		ctx = resource.WithResources(ctx, resource.NewStatic(resources))
	}

	result, err := engine.Execute(ctx, &req)
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
