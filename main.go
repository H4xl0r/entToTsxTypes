package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
	"entgo.io/ent/schema/field"
)

const (
	// go:generate runs from the ent/ directory.
	schemaPath = "../ent/schema"
	outputDir  = "../frontend/types"
	enumDir    = "../frontend/types/enums"
	mixinDir   = "../frontend/types/mixins"
)

func main() {
	graph, err := entc.LoadGraph(schemaPath, &gen.Config{})
	if err != nil {
		panic(err)
	}

	enumRegistry := buildEnumRegistry(graph)
	mixins := discoverMixins(schemaPath)

	// Discover entity mixin usage.
	entityMixins := make(map[string][]string)

	for _, node := range graph.Nodes {
		entityMixins[node.Name] = discoverEntityMixins(
			schemaPath,
			node.Name,
		)
	}

	// Recreate generated directories.
	if err := os.RemoveAll(outputDir); err != nil {
		panic(err)
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		panic(err)
	}

	if err := os.MkdirAll(enumDir, 0755); err != nil {
		panic(err)
	}

	if err := os.MkdirAll(mixinDir, 0755); err != nil {
		panic(err)
	}

	// Generate mixins.
	for name, mixin := range mixins {
		if err := renderMixin(mixin, name, enumRegistry); err != nil {
			panic(err)
		}
	}

	// Generate entities.
	for _, node := range graph.Nodes {
		if err := renderEntity(
			node,
			enumRegistry,
			entityMixins[node.Name],
			mixins,
		); err != nil {
			panic(err)
		}
	}

	// Generate enums.
	for _, enum := range enumRegistry.enums {
		if err := renderEnum(enum); err != nil {
			panic(err)
		}
	}

	if err := renderIndex(
		graph,
		enumRegistry,
		mixins,
	); err != nil {
		panic(err)
	}
}

type enumRegistry struct {
	fields map[string]string
	enums  map[string]string
}

func buildEnumRegistry(graph *gen.Graph) *enumRegistry {
	registry := &enumRegistry{
		fields: make(map[string]string),
		enums:  make(map[string]string),
	}

	for _, node := range graph.Nodes {
		for _, f := range node.Fields {
			if !f.IsEnum() {
				continue
			}

			enumName := ""

			if f.HasGoType() && f.Type.Ident != "" {
				enumName = lastIdentifier(f.Type.Ident)
			}

			if enumName == "" {
				enumName = pascalCase(node.Name) + pascalCase(f.Name)
			}

			registry.fields[node.Name+"."+f.Name] = enumName
			registry.enums[enumName] = enumName
		}
	}

	return registry
}

func discoverMixins(schemaRoot string) map[string]*gen.Type {
	result := make(map[string]*gen.Type)

	mixinPath := filepath.Join(schemaRoot, "mixin")

	entries, err := os.ReadDir(mixinPath)
	if err != nil {
		return result
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		path := filepath.Join(mixinPath, entry.Name())

		fset := token.NewFileSet()

		file, err := parser.ParseFile(
			fset,
			path,
			nil,
			parser.ParseComments,
		)
		if err != nil {
			continue
		}

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}

			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}

				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}

				if !embedsMixinSchema(structType) {
					continue
				}

				name := typeSpec.Name.Name

				// Keep the existing architecture.
				//
				// The actual mixin fields are discovered separately
				// from the Fields() method.
				result[name] = &gen.Type{
					Name: name,
				}
			}
		}
	}

	return result
}

func embedsMixinSchema(structType *ast.StructType) bool {
	for _, field := range structType.Fields.List {
		if field.Names != nil {
			continue
		}

		switch expr := field.Type.(type) {
		case *ast.SelectorExpr:
			if ident, ok := expr.X.(*ast.Ident); ok {
				if ident.Name == "mixin" &&
					expr.Sel.Name == "Schema" {
					return true
				}
			}

		case *ast.Ident:
			if expr.Name == "Schema" {
				return true
			}
		}
	}

	return false
}

func discoverEntityMixins(
	schemaRoot string,
	entityName string,
) []string {
	var result []string

	path := filepath.Join(
		schemaRoot,
		entityName+".go",
	)

	src, err := os.ReadFile(path)
	if err != nil {
		return result
	}

	fset := token.NewFileSet()

	file, err := parser.ParseFile(
		fset,
		path,
		src,
		parser.ParseComments,
	)
	if err != nil {
		return result
	}

	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		if funcDecl.Name.Name != "Mixin" {
			continue
		}

		if funcDecl.Body == nil {
			continue
		}

		ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
			composite, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}

			switch expr := composite.Type.(type) {
			case *ast.Ident:
				result = append(result, expr.Name)

			case *ast.SelectorExpr:
				if ident, ok := expr.X.(*ast.Ident); ok {
					result = append(
						result,
						ident.Name+expr.Sel.Name,
					)
				}
			}

			return true
		})
	}

	return result
}

func renderMixin(
	mixin *gen.Type,
	name string,
	registry *enumRegistry,
) error {
	if mixin == nil {
		return nil
	}

	var b strings.Builder

	b.WriteString("export interface ")
	b.WriteString(name)
	b.WriteString(" {\n")

	switch name {
	case "IDMixin":
		b.WriteString("  id: number;\n")

	case "TimeMixin":
		b.WriteString("  created_at: string;\n")
		b.WriteString("  updated_at: string;\n")

	case "BaseHashMixin":
		b.WriteString("  sha256: string;\n")
		b.WriteString("  secondary_sha256?: string | null;\n")

	case "JobMixin":
		b.WriteString("  state: JobState;\n")
		b.WriteString("  status: string;\n")
		b.WriteString("  priority: number;\n")
		b.WriteString("  attempts: number;\n")
		b.WriteString("  max_attempts: number;\n")
		b.WriteString("  started_at?: string | null;\n")
		b.WriteString("  finished_at?: string | null;\n")
		b.WriteString("  error?: string | null;\n")
	}

	b.WriteString("}\n")

	imports := ""

	if name == "JobMixin" {
		imports = `import type { JobState } from "./../enums/job-state";

`
	}

	path := filepath.Join(
		mixinDir,
		kebabCase(name)+".ts",
	)

	return os.WriteFile(
		path,
		[]byte(imports+b.String()),
		0644,
	)
}

func renderEntity(
	node *gen.Type,
	registry *enumRegistry,
	appliedMixins []string,
	mixins map[string]*gen.Type,
) error {
	var b strings.Builder

	imports := make(map[string]string)

	for _, edge := range node.Edges {
		if edge == nil || edge.Type == nil {
			continue
		}

		imports[edge.Type.Name] =
			"./" + kebabCase(edge.Type.Name)
	}

	for _, mixinName := range appliedMixins {
		imports[mixinName] =
			"./mixins/" + kebabCase(mixinName)
	}

	for _, f := range node.Fields {
		if f == nil || !f.IsEnum() {
			continue
		}

		enumName := registry.fields[node.Name+"."+f.Name]

		if enumName != "" {
			imports[enumName] =
				"./enums/" + kebabCase(enumName)
		}
	}

	var importNames []string

	for name := range imports {
		importNames = append(
			importNames,
			name,
		)
	}

	sort.Strings(importNames)

	for _, name := range importNames {
		b.WriteString("import type { ")
		b.WriteString(name)
		b.WriteString(" } from \"")
		b.WriteString(imports[name])
		b.WriteString("\";\n")
	}

	if len(importNames) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("export interface ")
	b.WriteString(node.Name)

	for _, mixinName := range appliedMixins {
		b.WriteString(" extends ")
		b.WriteString(mixinName)
	}

	b.WriteString(" {\n")

	// Ent's implicit ID.
	if !hasField(node.Fields, "id") &&
		!mixinOwnsField(
			"id",
			appliedMixins,
			mixins,
		) {
		b.WriteString("  id: number;\n")
	}

	for _, f := range node.Fields {
		if f == nil {
			continue
		}

		if mixinOwnsField(
			f.Name,
			appliedMixins,
			mixins,
		) {
			continue
		}

		typ, err := fieldType(
			f,
			node,
			registry,
		)
		if err != nil {
			return err
		}

		optional := ""

		if f.Optional {
			optional = "?"
		}

		b.WriteString("  ")
		b.WriteString(f.Name)
		b.WriteString(optional)
		b.WriteString(": ")
		b.WriteString(typ)
		b.WriteString(";\n")
	}

	if len(node.Edges) > 0 {
		b.WriteString("\n")
		b.WriteString("  edges: {\n")

		for _, edge := range node.Edges {
			if edge == nil || edge.Type == nil {
				continue
			}

			b.WriteString("    ")
			b.WriteString(edge.Name)
			b.WriteString("?: ")

			if edge.Unique {
				b.WriteString(edge.Type.Name)
			} else {
				b.WriteString(edge.Type.Name)
				b.WriteString("[]")
			}

			b.WriteString(";\n")
		}

		b.WriteString("  };\n")
	}

	b.WriteString("}\n")

	path := filepath.Join(
		outputDir,
		kebabCase(node.Name)+".ts",
	)

	return os.WriteFile(
		path,
		[]byte(b.String()),
		0644,
	)
}

func fieldType(
	f *gen.Field,
	node *gen.Type,
	registry *enumRegistry,
) (string, error) {
	if f == nil {
		return "unknown", nil
	}

	if f.IsEnum() {
		name := registry.fields[node.Name+"."+f.Name]

		if name == "" {
			return "", fmt.Errorf(
				"enum registry entry missing for %s.%s",
				node.Name,
				f.Name,
			)
		}

		return name, nil
	}

	if f.Type == nil {
		return "unknown", nil
	}

	switch f.Type.Type {
	case field.TypeBool:
		return "boolean", nil

	case field.TypeString:
		return "string", nil

	case field.TypeTime:
		return "string", nil

	case field.TypeUUID:
		return "string", nil

	case field.TypeBytes:
		return "string", nil

	case field.TypeJSON:
		return jsonGoTypeToTS(f)

	case field.TypeInt8,
		field.TypeInt16,
		field.TypeInt32,
		field.TypeInt,
		field.TypeInt64,
		field.TypeUint8,
		field.TypeUint16,
		field.TypeUint32,
		field.TypeUint,
		field.TypeUint64,
		field.TypeFloat32,
		field.TypeFloat64:

		return "number", nil

	case field.TypeOther:
		if f.Type.Ident != "" {
			return lastIdentifier(
				f.Type.Ident,
			), nil
		}

		return "unknown", nil

	default:
		return "unknown", nil
	}
}

/*
JSON handling

This is the only important new part.

For:

	field.JSON("api_allow_origins", []string{})

Ent exposes the actual Go type through:

	f.Type.RType.Ident

which gives:

	[]string

so we can correctly generate:

	string[]
*/
func jsonGoTypeToTS(
	f *gen.Field,
) (string, error) {
	if f == nil || f.Type == nil {
		return "unknown", nil
	}

	ident := ""

	if f.Type.RType != nil {
		ident = f.Type.RType.Ident
	}

	if ident == "" {
		ident = f.Type.Ident
	}

	ident = normalizeGoType(ident)

	switch ident {
	case "string":
		return "string", nil

	case "bool":
		return "boolean", nil

	case "int",
		"int8",
		"int16",
		"int32",
		"int64",
		"uint",
		"uint8",
		"uint16",
		"uint32",
		"uint64",
		"float32",
		"float64":

		return "number", nil

	case "[]string":
		return "string[]", nil

	case "[]bool":
		return "boolean[]", nil

	case "[]int",
		"[]int8",
		"[]int16",
		"[]int32",
		"[]int64",
		"[]uint",
		"[]uint8",
		"[]uint16",
		"[]uint32",
		"[]uint64",
		"[]float32",
		"[]float64":

		return "number[]", nil

	case "[]any",
		"[]interface{}":

		return "unknown[]", nil

	case "map[string]string":
		return "Record<string, string>", nil

	case "map[string]bool":
		return "Record<string, boolean>", nil

	case "map[string]int",
		"map[string]int8",
		"map[string]int16",
		"map[string]int32",
		"map[string]int64",
		"map[string]uint",
		"map[string]uint8",
		"map[string]uint16",
		"map[string]uint32",
		"map[string]uint64",
		"map[string]float32",
		"map[string]float64":

		return "Record<string, number>", nil

	case "map[string]any",
		"map[string]interface{}":

		return "Record<string, unknown>", nil

	case "any",
		"interface{}",
		"json.RawMessage":

		return "unknown", nil

	default:
		return goCompositeTypeToTS(ident), nil
	}
}

func normalizeGoType(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")

	for strings.HasPrefix(s, "*") {
		s = strings.TrimPrefix(s, "*")
	}

	return s
}

func goCompositeTypeToTS(
	ident string,
) string {
	ident = normalizeGoType(ident)

	if ident == "" {
		return "unknown"
	}

	if strings.HasPrefix(ident, "[]") {
		elem := strings.TrimPrefix(
			ident,
			"[]",
		)

		switch elem {
		case "string":
			return "string[]"

		case "bool":
			return "boolean[]"

		case "int",
			"int8",
			"int16",
			"int32",
			"int64",
			"uint",
			"uint8",
			"uint16",
			"uint32",
			"uint64",
			"float32",
			"float64":

			return "number[]"

		case "any",
			"interface{}":

			return "unknown[]"
		}

		return "unknown[]"
	}

	if strings.HasPrefix(
		ident,
		"map[string]",
	) {
		valueType := strings.TrimPrefix(
			ident,
			"map[string]",
		)

		switch valueType {
		case "string":
			return "Record<string, string>"

		case "bool":
			return "Record<string, boolean>"

		case "int",
			"int8",
			"int16",
			"int32",
			"int64",
			"uint",
			"uint8",
			"uint16",
			"uint32",
			"uint64",
			"float32",
			"float64":

			return "Record<string, number>"

		case "any",
			"interface{}":

			return "Record<string, unknown>"
		}

		return "Record<string, unknown>"
	}

	return "unknown"
}

func renderEnum(enumName string) error {
	var b strings.Builder

	b.WriteString("export enum ")
	b.WriteString(enumName)
	b.WriteString(" {\n")
	b.WriteString("}\n")

	path := filepath.Join(
		enumDir,
		kebabCase(enumName)+".ts",
	)

	return os.WriteFile(
		path,
		[]byte(b.String()),
		0644,
	)
}

func renderIndex(
	graph *gen.Graph,
	registry *enumRegistry,
	mixins map[string]*gen.Type,
) error {
	var b strings.Builder

	nodes := make([]string, 0, len(graph.Nodes))

	for _, node := range graph.Nodes {
		nodes = append(
			nodes,
			node.Name,
		)
	}

	sort.Strings(nodes)

	for _, name := range nodes {
		b.WriteString("export type { ")
		b.WriteString(name)
		b.WriteString(" } from \"./")
		b.WriteString(kebabCase(name))
		b.WriteString("\";\n")
	}

	b.WriteString("\n")

	mixinNames := make([]string, 0, len(mixins))

	for name := range mixins {
		mixinNames = append(
			mixinNames,
			name,
		)
	}

	sort.Strings(mixinNames)

	for _, name := range mixinNames {
		b.WriteString("export type { ")
		b.WriteString(name)
		b.WriteString(" } from \"./mixins/")
		b.WriteString(kebabCase(name))
		b.WriteString("\";\n")
	}

	b.WriteString("\n")

	enumNames := make([]string, 0, len(registry.enums))

	for name := range registry.enums {
		enumNames = append(
			enumNames,
			name,
		)
	}

	sort.Strings(enumNames)

	for _, name := range enumNames {
		b.WriteString("export { ")
		b.WriteString(name)
		b.WriteString(" } from \"./enums/")
		b.WriteString(kebabCase(name))
		b.WriteString("\";\n")
	}

	return os.WriteFile(
		filepath.Join(
			outputDir,
			"index.ts",
		),
		[]byte(b.String()),
		0644,
	)
}

func hasField(
	fields []*gen.Field,
	name string,
) bool {
	for _, f := range fields {
		if f == nil {
			continue
		}

		if f.Name == name {
			return true
		}
	}

	return false
}

func mixinOwnsField(
	fieldName string,
	appliedMixins []string,
	mixins map[string]*gen.Type,
) bool {
	for _, mixinName := range appliedMixins {
		if _, ok := mixins[mixinName]; !ok {
			continue
		}

		switch mixinName {
		case "IDMixin":
			if fieldName == "id" {
				return true
			}

		case "TimeMixin":
			if fieldName == "created_at" ||
				fieldName == "updated_at" {
				return true
			}

		case "BaseHashMixin":
			if fieldName == "sha256" ||
				fieldName == "secondary_sha256" {
				return true
			}

		case "JobMixin":
			switch fieldName {
			case "state",
				"status",
				"priority",
				"attempts",
				"max_attempts",
				"started_at",
				"finished_at",
				"error":
				return true
			}
		}
	}

	return false
}

func lastIdentifier(s string) string {
	s = strings.TrimSpace(s)

	if s == "" {
		return ""
	}

	if idx := strings.LastIndex(s, "."); idx >= 0 {
		return s[idx+1:]
	}

	return s
}

func pascalCase(s string) string {
	words := splitWords(s)

	var b strings.Builder

	for _, word := range words {
		if word == "" {
			continue
		}

		runes := []rune(word)

		if len(runes) == 0 {
			continue
		}

		b.WriteRune(
			unicode.ToUpper(runes[0]),
		)

		for _, r := range runes[1:] {
			b.WriteRune(r)
		}
	}

	return b.String()
}

func kebabCase(s string) string {
	words := splitWords(s)

	return strings.ToLower(
		strings.Join(words, "-"),
	)
}

func splitWords(s string) []string {
	if s == "" {
		return nil
	}

	var words []string
	var current []rune

	flush := func() {
		if len(current) == 0 {
			return
		}

		words = append(
			words,
			string(current),
		)

		current = nil
	}

	runes := []rune(s)

	for i, r := range runes {
		if r == '_' ||
			r == '-' ||
			r == ' ' ||
			r == '.' {
			flush()
			continue
		}

		if unicode.IsUpper(r) &&
			len(current) > 0 {

			prev := runes[i-1]

			if unicode.IsLower(prev) ||
				unicode.IsDigit(prev) {
				flush()
			}
		}

		current = append(
			current,
			r,
		)
	}

	flush()

	return words
}

func sameStrings(
	a, b []string,
) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func tsString(s string) string {
	s = strings.ReplaceAll(
		s,
		"\\",
		"\\\\",
	)

	s = strings.ReplaceAll(
		s,
		"\"",
		"\\\"",
	)

	return "\"" + s + "\""
}
