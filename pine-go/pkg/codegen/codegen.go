// Package codegen generates Apple Python operator bindings and Markdown
// documentation from registered Go OperatorSchema entries.
//
// Third-party projects import this package and call [Run] from a thin
// main.go that also blank-imports their custom operator packages.
package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Liam0205/pineapple/pine-go/internal/registry"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

// Config holds the code-generation settings.
type Config struct {
	OutputDir string // Python output directory (e.g. "apple_generated")
	DocDir    string // Markdown doc output directory; empty to skip
	OpsDir    string // Go operator source directory for doc-comment parsing
}

// EnrichedSchema augments OperatorSchema with row-set marker bools probed
// from the operator factory. Markers are interface-based contracts (declared
// by embedding *Marker structs) and are the source of truth for whether an
// operator additively writes, consumes, or mutates the row set. We surface
// them here so the Apple validator can judge row-set semantics directly
// rather than via type-name prefix.
type EnrichedSchema struct {
	types.OperatorSchema
	AdditiveWritesRowSet bool
	ConsumesRowSet       bool
	MutatesRowSet        bool
}

// enrichSchemas probes every registered operator's marker interfaces by
// instantiating its factory once and packaging the bools alongside the
// schema for template consumption.
func enrichSchemas(schemas []types.OperatorSchema) []EnrichedSchema {
	out := make([]EnrichedSchema, 0, len(schemas))
	for _, s := range schemas {
		e := EnrichedSchema{OperatorSchema: s}
		if _, factory, ok := registry.Lookup(s.Name); ok && factory != nil {
			op := factory()
			if _, ok := op.(types.AdditiveWritesRowSet); ok {
				e.AdditiveWritesRowSet = true
			}
			if _, ok := op.(types.ConsumesRowSet); ok {
				e.ConsumesRowSet = true
			}
			if _, ok := op.(types.MutatesRowSet); ok {
				e.MutatesRowSet = true
			}
		}
		out = append(out, e)
	}
	return out
}

// Run reads all registered operator schemas from the global registry and
// generates Python bindings and (optionally) Markdown documentation.
func Run(cfg Config) error {
	schemas := registry.All()
	if len(schemas) == 0 {
		return fmt.Errorf("no operators registered")
	}

	// Ensure output directory exists
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", cfg.OutputDir, err)
	}

	opTmpl, initTmpl, err := parseTemplates()
	if err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}

	enriched := enrichSchemas(schemas)

	// Generate operators.py
	opPath := filepath.Join(cfg.OutputDir, "operators.py")
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
	initPath := filepath.Join(cfg.OutputDir, "__init__.py")
	initFile, err := os.Create(initPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", initPath, err)
	}
	defer initFile.Close()

	if err := initTmpl.Execute(initFile, schemas); err != nil {
		return fmt.Errorf("render __init__.py: %w", err)
	}
	fmt.Printf("generated %s\n", initPath)

	// Generate markers.py — operator → row-set marker bools, looked up at
	// runtime by Apple OpCall to populate additive_writes_row_set / etc.
	markersTmpl, err := parseMarkersTemplate()
	if err != nil {
		return fmt.Errorf("parse markers template: %w", err)
	}
	markersPath := filepath.Join(cfg.OutputDir, "markers.py")
	markersFile, err := os.Create(markersPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", markersPath, err)
	}
	defer markersFile.Close()
	if err := markersTmpl.Execute(markersFile, enriched); err != nil {
		return fmt.Errorf("render markers.py: %w", err)
	}
	fmt.Printf("generated %s\n", markersPath)

	// Generate operator documentation if doc-dir is specified
	if cfg.DocDir != "" {
		if err := generateDocs(cfg.DocDir, cfg.OpsDir, schemas); err != nil {
			return fmt.Errorf("generate docs: %w", err)
		}
	}

	// Generate resource classes if any resources are registered
	resSchemas := resource.All()
	if len(resSchemas) > 0 {
		if err := generateResources(cfg.OutputDir, resSchemas); err != nil {
			return fmt.Errorf("generate resources: %w", err)
		}
	}

	return nil
}

func generateResources(outputDir string, schemas []types.ResourceSchema) error {
	resTmpl, initTmpl, err := parseResourceTemplates()
	if err != nil {
		return fmt.Errorf("parse resource templates: %w", err)
	}

	// Generate resources.py
	resPath := filepath.Join(outputDir, "resources.py")
	resFile, err := os.Create(resPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", resPath, err)
	}
	defer resFile.Close()

	if err := resTmpl.Execute(resFile, schemas); err != nil {
		return fmt.Errorf("render resources.py: %w", err)
	}
	fmt.Printf("generated %s (%d resources)\n", resPath, len(schemas))

	// Generate resources_init.py (for __init__.py-style imports)
	initPath := filepath.Join(outputDir, "resources_init.py")
	initFile, err := os.Create(initPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", initPath, err)
	}
	defer initFile.Close()

	if err := initTmpl.Execute(initFile, schemas); err != nil {
		return fmt.Errorf("render resources_init.py: %w", err)
	}
	fmt.Printf("generated %s\n", initPath)

	return nil
}

func generateDocs(docDir, opsDir string, schemas []types.OperatorSchema) error {
	// Parse operator doc comments from source
	opDocs, err := ParseOperatorDocs(opsDir)
	if err != nil {
		return fmt.Errorf("parse operator docs: %w", err)
	}
	docMap := opDocByName(opDocs)

	// Build schema map for lookup
	schemaMap := make(map[string]types.OperatorSchema, len(schemas))
	for _, s := range schemas {
		schemaMap[s.Name] = s
	}

	// Parse doc templates
	docTmpl, idxTmpl, err := parseDocTemplates()
	if err != nil {
		return fmt.Errorf("parse doc templates: %w", err)
	}

	// Ensure output directory
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", docDir, err)
	}

	// Generate per-operator docs
	typeOps := make(map[string][]indexOp)
	for _, schema := range schemas {
		doc, hasDoc := docMap[schema.Name]
		data := buildDocData(schema, doc, hasDoc)

		fpath := filepath.Join(docDir, schema.Name+".md")
		f, err := os.Create(fpath)
		if err != nil {
			return fmt.Errorf("create %s: %w", fpath, err)
		}
		if err := docTmpl.Execute(f, data); err != nil {
			f.Close()
			return fmt.Errorf("render %s: %w", fpath, err)
		}
		f.Close()

		opType := data.Type
		if opType == "" {
			opType = "Other"
		}
		typeOps[opType] = append(typeOps[opType], indexOp{
			Name:        data.Name,
			Description: data.Description,
		})
	}

	// Sort type groups
	typeNames := make([]string, 0, len(typeOps))
	for c := range typeOps {
		typeNames = append(typeNames, c)
	}
	sort.Strings(typeNames)

	var categories []indexCategory
	for _, c := range typeNames {
		categories = append(categories, indexCategory{Name: c, Ops: typeOps[c]})
	}

	// Generate index
	idxPath := filepath.Join(docDir, "README.md")
	idxFile, err := os.Create(idxPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", idxPath, err)
	}
	defer idxFile.Close()

	if err := idxTmpl.Execute(idxFile, indexData{Categories: categories}); err != nil {
		return fmt.Errorf("render index: %w", err)
	}

	fmt.Printf("generated %d operator docs in %s\n", len(schemas), docDir)
	return nil
}

func buildDocData(schema types.OperatorSchema, doc OpDoc, hasDoc bool) DocData {
	data := DocData{
		Name:        schema.Name,
		Type:        string(schema.Type),
		Description: schema.Description,
	}

	if hasDoc {
		data.Metadata = doc.Metadata
	}

	// Build params from schema (authoritative for all fields now)
	paramNames := sortedParams(schema.Params)
	for _, name := range paramNames {
		spec := schema.Params[name]
		dp := DocParam{
			Name:        name,
			Type:        spec.Type,
			Required:    spec.Required,
			Description: spec.Description,
		}
		if spec.Default != nil {
			dp.Default = pythonLiteral(spec.Default)
		}
		data.Params = append(data.Params, dp)
	}

	return data
}
