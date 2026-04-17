package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// OpDoc holds documentation extracted from a Go operator source file's
// package-level doc comment.
type OpDoc struct {
	Name        string
	Category    string
	Description string
	ParamDocs   []ParamDoc
	Metadata    MetadataDoc
}

// ParamDoc holds the human-readable documentation for a single parameter.
type ParamDoc struct {
	Name        string
	Type        string
	Required    bool
	Default     string
	Description string
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
func parseDocComment(text string) (OpDoc, bool, error) {
	lines := strings.Split(text, "\n")

	var doc OpDoc
	found := false

	// State machine for parsing sections
	const (
		sectionNone = iota
		sectionDescription
		sectionParams
		sectionMetadata
	)
	section := sectionNone

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for section headers
		if strings.HasPrefix(trimmed, "Operator:") {
			doc.Name = strings.TrimSpace(strings.TrimPrefix(trimmed, "Operator:"))
			found = true
			section = sectionNone
			continue
		}
		if strings.HasPrefix(trimmed, "Category:") {
			doc.Category = strings.TrimSpace(strings.TrimPrefix(trimmed, "Category:"))
			section = sectionNone
			continue
		}
		if strings.HasPrefix(trimmed, "Description:") {
			doc.Description = strings.TrimSpace(strings.TrimPrefix(trimmed, "Description:"))
			section = sectionDescription
			continue
		}
		if strings.HasPrefix(trimmed, "Params:") {
			section = sectionParams
			continue
		}
		if strings.HasPrefix(trimmed, "Metadata contract") {
			section = sectionMetadata
			continue
		}

		// Process lines based on current section
		switch section {
		case sectionDescription:
			if trimmed == "" {
				section = sectionNone
			} else {
				doc.Description += " " + trimmed
			}

		case sectionParams:
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "- ") {
				pd := parseParamLine(trimmed)
				doc.ParamDocs = append(doc.ParamDocs, pd)
			} else if !strings.HasPrefix(trimmed, "Exactly") && !strings.HasPrefix(trimmed, "Note") {
				// Extra line for a note about params, skip section change
			}

		case sectionMetadata:
			if trimmed == "" {
				continue
			}
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

// parseParamLine parses a line like:
//
//	- field (string, required): Item field to check.
//	- output_field (string, optional, default=<field>+"_norm"): Target field.
func parseParamLine(line string) ParamDoc {
	// Remove leading "- "
	line = strings.TrimPrefix(line, "- ")

	pd := ParamDoc{}

	// Split at first ":"
	colonIdx := strings.Index(line, "):")
	if colonIdx == -1 {
		// No description, try to parse just the header
		colonIdx = strings.Index(line, ")")
		if colonIdx == -1 {
			pd.Name = line
			return pd
		}
	}

	header := line[:colonIdx+1]
	if colonIdx+1 < len(line) {
		desc := line[colonIdx+1:]
		if strings.HasPrefix(desc, ":") {
			desc = desc[1:]
		}
		pd.Description = strings.TrimSpace(desc)
	}

	// Parse header: "field (string, required)"
	parenIdx := strings.Index(header, "(")
	if parenIdx == -1 {
		pd.Name = strings.TrimSpace(header)
		return pd
	}

	pd.Name = strings.TrimSpace(header[:parenIdx])
	spec := strings.TrimSuffix(strings.TrimSpace(header[parenIdx+1:]), ")")

	parts := strings.Split(spec, ",")
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if i == 0 {
			pd.Type = part
		} else if part == "required" {
			pd.Required = true
		} else if part == "optional" {
			pd.Required = false
		} else if strings.HasPrefix(part, "default") {
			// Extract default value: "default=xxx" or "default \"xxx\""
			eqIdx := strings.Index(part, "=")
			if eqIdx != -1 {
				pd.Default = strings.TrimSpace(part[eqIdx+1:])
			} else if strings.HasPrefix(part, "default ") {
				pd.Default = strings.TrimSpace(strings.TrimPrefix(part, "default "))
			}
		}
	}

	return pd
}

// opDocByName builds a name-keyed map from a slice of OpDocs.
func opDocByName(docs []OpDoc) map[string]OpDoc {
	m := make(map[string]OpDoc, len(docs))
	for _, d := range docs {
		m[d.Name] = d
	}
	return m
}

// hasOpDocComment checks if an ast.File has a doc comment containing "Operator:".
func hasOpDocComment(f *ast.File) bool {
	if f.Doc != nil {
		return strings.Contains(f.Doc.Text(), "Operator:")
	}
	return false
}
