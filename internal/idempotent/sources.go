package idempotent

// knownSources inventories the map-iteration sites the generation pipeline
// traverses while turning a schema and queries into files. Each entry records
// where the map is ranged and what keeps the generated output deterministic.
//
// Status values:
//   - "sorted": the ranged values are explicitly sorted before they can reach
//     a template or the response, so map order cannot leak into output.
//   - "fallback": no natural declaration order exists; a stable sort is the
//     authoritative order and a hint is emitted when it is relied upon.
func knownSources() []NondeterminismSource {
	return []NondeterminismSource{
		{
			Package: "codegen/golang",
			File:    "internal/codegen/golang/gen.go",
			Line:    334,
			Code:    "for source := range files",
			Reason:  "query source files are collected in a map[string]struct{} before rendering; sorted by source name so query-file generation order is stable",
			Status:  "sorted",
		},
		{
			Package: "codegen/golang",
			File:    "internal/codegen/golang/gen.go",
			Line:    348,
			Code:    "for filename := range output",
			Reason:  "rendered files are held in a map[string]string; sorted by filename before being attached to GenerateResponse",
			Status:  "sorted",
		},
		{
			Package: "codegen/golang",
			File:    "internal/codegen/golang/imports.go",
			Line:    336,
			Code:    "for spec := range pkg",
			Reason:  "third-party import set is a map[ImportSpec]struct{}; sortedImports sorts specs by import path before rendering",
			Status:  "sorted",
		},
		{
			Package: "codegen/golang",
			File:    "internal/codegen/golang/imports.go",
			Line:    340,
			Code:    "for path := range std",
			Reason:  "standard-library import set is a map[string]struct{}; sortedImports sorts paths before rendering",
			Status:  "sorted",
		},
		{
			Package: "codegen/golang",
			File:    "internal/codegen/golang/field.go",
			Line:    40,
			Code:    "for key, val := range tags",
			Reason:  "struct tags are a map[string]string; TagsToString sorts tag keys so struct tag order is stable",
			Status:  "sorted",
		},
		{
			Package: "cmd",
			File:    "internal/cmd/generate.go",
			Line:    202,
			Code:    "for _, file := range resp.Files",
			Reason:  "plugin files are merged into the output map; writeFiles sorts filenames again before they touch disk",
			Status:  "fallback",
		},
	}
}
