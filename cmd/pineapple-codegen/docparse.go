package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// OpDoc holds the metadata contract extracted from a Go operator source file's
// package-level doc comment. Category, Description, and param descriptions are
// now part of OperatorSchema and enforced at registration time; only the
// metadata contract (best-effort) is still parsed from comments.
type OpDoc struct {
	Name     string
	Metadata MetadataDoc
}

// MetadataDoc holds the typical metadata contract extracted from comments.
type MetadataDoc struct {
	CommonInput  string
	CommonOutput string
	ItemInput    string
	ItemOutput   string
}

// ParseOperatorDocs scans all .go files under rootDir (recursively), extracts
// package-level doc comments, and returns an OpDoc for each file that contains
// an "// Operator:" annotation.
func ParseOperatorDocs(rootDir string) ([]OpDoc, error) {
	var docs []OpDoc

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip test files and aggregation file
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") || base == "all.go" {
			return nil
		}

		doc, ok, parseErr := parseFileDoc(path)
		if parseErr != nil {
			return parseErr
		}
		if ok {
			docs = append(docs, doc)
		}
		return nil
	})

	return docs, err
}

// parseFileDoc reads a single Go file and extracts an OpDoc from its
// package-level doc comment. Returns ok=false if the file has no
// "// Operator:" annotation.
func parseFileDoc(path string) (OpDoc, bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return OpDoc{}, false, err
	}

	// Get package-level doc comment
	docText := ""
	if f.Doc != nil {
		docText = f.Doc.Text()
	} else {
		// Fall back: check first comment group before package declaration
		for _, cg := range f.Comments {
			if cg.End() <= f.Package {
				docText = cg.Text()
				break
			}
		}
	}

	if docText == "" {
		return OpDoc{}, false, nil
	}

	return parseDocComment(docText)
}

// parseDocComment parses the text of a doc comment into an OpDoc.
// Only the Operator name and Metadata contract section are extracted;
// Category, Description, and Params are now in OperatorSchema.
func parseDocComment(text string) (OpDoc, bool, error) {
	lines := strings.Split(text, "\n")

	var doc OpDoc
	found := false
	inMetadata := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "Operator:") {
			doc.Name = strings.TrimSpace(strings.TrimPrefix(trimmed, "Operator:"))
			found = true
			continue
		}
		if strings.HasPrefix(trimmed, "Metadata contract") {
			inMetadata = true
			continue
		}
		// Any other section header ends metadata parsing
		if inMetadata && trimmed != "" && !strings.HasPrefix(trimmed, "CommonInput:") &&
			!strings.HasPrefix(trimmed, "CommonOutput:") &&
			!strings.HasPrefix(trimmed, "ItemInput:") &&
			!strings.HasPrefix(trimmed, "ItemOutput:") {
			// Check if this is a known section header that ends metadata
			if strings.HasPrefix(trimmed, "Category:") || strings.HasPrefix(trimmed, "Description:") ||
				strings.HasPrefix(trimmed, "Params:") || strings.HasPrefix(trimmed, "Operator:") {
				inMetadata = false
			}
		}

		if inMetadata {
			if strings.HasPrefix(trimmed, "CommonInput:") {
				doc.Metadata.CommonInput = strings.TrimSpace(strings.TrimPrefix(trimmed, "CommonInput:"))
			} else if strings.HasPrefix(trimmed, "CommonOutput:") {
				doc.Metadata.CommonOutput = strings.TrimSpace(strings.TrimPrefix(trimmed, "CommonOutput:"))
			} else if strings.HasPrefix(trimmed, "ItemInput:") {
				doc.Metadata.ItemInput = strings.TrimSpace(strings.TrimPrefix(trimmed, "ItemInput:"))
			} else if strings.HasPrefix(trimmed, "ItemOutput:") {
				doc.Metadata.ItemOutput = strings.TrimSpace(strings.TrimPrefix(trimmed, "ItemOutput:"))
			}
		}
	}

	return doc, found, nil
}

// opDocByName builds a name-keyed map from a slice of OpDocs.
func opDocByName(docs []OpDoc) map[string]OpDoc {
	m := make(map[string]OpDoc, len(docs))
	for _, d := range docs {
		m[d.Name] = d
	}
	return m
}
