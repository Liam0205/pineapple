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

	"github.com/Liam0205/pineapple/internal/registry"
	"github.com/Liam0205/pineapple/internal/types"
)

// Config holds the code-generation settings.
type Config struct {
	OutputDir string // Python output directory (e.g. "apple/generated")
	DocDir    string // Markdown doc output directory; empty to skip
	OpsDir    string // Go operator source directory for doc-comment parsing
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

	// Generate operator documentation if doc-dir is specified
	if cfg.DocDir != "" {
		if err := generateDocs(cfg.DocDir, cfg.OpsDir, schemas); err != nil {
			return fmt.Errorf("generate docs: %w", err)
		}
	}

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
	categoryOps := make(map[string][]indexOp)
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

		cat := data.Category
		if cat == "" {
			cat = "Other"
		}
		categoryOps[cat] = append(categoryOps[cat], indexOp{
			Name:        data.Name,
			Description: data.Description,
		})
	}

	// Sort categories
	catNames := make([]string, 0, len(categoryOps))
	for c := range categoryOps {
		catNames = append(catNames, c)
	}
	sort.Strings(catNames)

	var categories []indexCategory
	for _, c := range catNames {
		categories = append(categories, indexCategory{Name: c, Ops: categoryOps[c]})
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
		Category:    schema.Category,
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
