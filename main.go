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

type importSpec struct {
	Name string
	Path string
}

type enumDefinition struct {
	Name   string
	Values []string
}

type enumRegistry struct {
	fields      map[string]string
	definitions map[string]enumDefinition
}

type mixinDefinition struct {
	Name       string
	Fields     []*gen.Field
	EnumFields []string
}

type entityMixins map[string][]string

func main() {
	graph, err := entc.LoadGraph(schemaPath, &gen.Config{})
	if err != nil {
		exitErr("load Ent graph", err)
	}

	registry, err := buildEnumRegistry(graph)
	if err != nil {
		exitErr("build enum registry", err)
	}

	mixins, err := discoverMixins()
	if err != nil {
		exitErr("discover mixins", err)
	}

	entityMixinMap, err := discoverEntityMixins()
	if err != nil {
		exitErr("discover entity mixins", err)
	}

	if err := os.RemoveAll(outputDir); err != nil {
		exitErr("remove old generated types", err)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		exitErr("create output directory", err)
	}

	if err := os.MkdirAll(enumDir, 0o755); err != nil {
		exitErr("create enum directory", err)
	}

	if err := os.MkdirAll(mixinDir, 0o755); err != nil {
		exitErr("create mixin directory", err)
	}

	if err := generateMixins(mixins, registry); err != nil {
		exitErr("generate mixins", err)
	}

	if err := generateEntities(
		graph,
		registry,
		mixins,
		entityMixinMap,
	); err != nil {
		exitErr("generate entities", err)
	}

	if err := generateEnums(registry); err != nil {
		exitErr("generate enums", err)
	}

	if err := generateIndex(
		graph,
		registry,
		mixins,
	); err != nil {
		exitErr("generate index", err)
	}
}

func exitErr(action string, err error) {
	fmt.Fprintf(
		os.Stderr,
		"ent-tsgen: %s: %v\n",
		action,
		err,
	)

	os.Exit(1)
}

// =============================================================================
// ENUMS
// =============================================================================

func buildEnumRegistry(graph *gen.Graph) (*enumRegistry, error) {
	registry := &enumRegistry{
		fields:      make(map[string]string),
		definitions: make(map[string]enumDefinition),
	}

	for _, node := range graph.Nodes {
		for _, f := range node.Fields {
			if !f.IsEnum() {
				continue
			}

			values := f.EnumValues()
			name := enumNameForField(node, f)

			key := node.Name + "." + f.Name
			registry.fields[key] = name

			if existing, ok := registry.definitions[name]; ok {
				if !sameStrings(existing.Values, values) {
					return nil, fmt.Errorf(
						"enum %q has conflicting values between fields; %s.%s has %v but existing definition has %v",
						name,
						node.Name,
						f.Name,
						values,
						existing.Values,
					)
				}

				continue
			}

			registry.definitions[name] = enumDefinition{
				Name:   name,
				Values: append([]string(nil), values...),
			}
		}
	}

	return registry, nil
}

func enumNameForField(node *gen.Type, f *gen.Field) string {
	// Custom Go enum:
	//
	// field.Enum("state").
	//     GoType(types.JobState(""))
	//
	// => JobState
	if f.HasGoType() && f.Type != nil && f.Type.Ident != "" {
		return lastIdentifier(f.Type.Ident)
	}

	// Normal Ent enum:
	//
	// Message.source => MessageSource
	return pascalCase(node.Name) + pascalCase(f.Name)
}

// =============================================================================
// MIXIN DISCOVERY
// =============================================================================

func discoverMixins() (map[string]*mixinDefinition, error) {
	result := make(map[string]*mixinDefinition)

	mixinPath := filepath.Join(schemaPath, "mixin")

	entries, err := os.ReadDir(mixinPath)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}

		return nil, err
	}

	fset := token.NewFileSet()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		if strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		filename := filepath.Join(
			mixinPath,
			entry.Name(),
		)

		file, err := parser.ParseFile(
			fset,
			filename,
			nil,
			parser.ParseComments,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"parse mixin %s: %w",
				filename,
				err,
			)
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

				if !isEntMixinStruct(structType) {
					continue
				}

				name := typeSpec.Name.Name

				result[name] = &mixinDefinition{
					Name: name,
				}
			}
		}
	}

	if len(result) > 0 {
		loadMixinFields(result)
	}

	return result, nil
}

func isEntMixinStruct(st *ast.StructType) bool {
	if st.Fields == nil {
		return false
	}

	for _, field := range st.Fields.List {
		// Embedded mixin.Schema.
		if len(field.Names) > 0 {
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

// loadMixinFields resolves mixin fields from the schema source.
func loadMixinFields(
	mixins map[string]*mixinDefinition,
) {
	mixinPath := filepath.Join(schemaPath, "mixin")

	entries, err := os.ReadDir(mixinPath)
	if err != nil {
		return
	}

	fset := token.NewFileSet()

	for _, entry := range entries {
		if entry.IsDir() ||
			!strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		filename := filepath.Join(
			mixinPath,
			entry.Name(),
		)

		file, err := parser.ParseFile(
			fset,
			filename,
			nil,
			0,
		)
		if err != nil {
			continue
		}

		for _, decl := range file.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}

			if funcDecl.Name.Name != "Fields" ||
				funcDecl.Recv == nil ||
				len(funcDecl.Recv.List) != 1 {
				continue
			}

			receiverName := receiverTypeName(
				funcDecl.Recv.List[0].Type,
			)

			mixin, ok := mixins[receiverName]
			if !ok {
				continue
			}

			for _, name := range extractFieldNames(funcDecl) {
				mixin.Fields = append(
					mixin.Fields,
					&gen.Field{
						Name: name,
					},
				)
			}
		}
	}
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name

	case *ast.StarExpr:
		return receiverTypeName(t.X)

	default:
		return ""
	}
}

func extractFieldNames(fn *ast.FuncDecl) []string {
	var result []string

	if fn.Body == nil {
		return result
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		switch sel.Sel.Name {
		case "String",
			"Bool",
			"Int",
			"Int8",
			"Int16",
			"Int32",
			"Int64",
			"Uint",
			"Uint8",
			"Uint16",
			"Uint32",
			"Uint64",
			"Float32",
			"Float64",
			"Time",
			"JSON",
			"Bytes",
			"UUID",
			"Enum":

			if len(call.Args) == 0 {
				return true
			}

			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}

			name := strings.Trim(
				lit.Value,
				`"`,
			)

			result = append(result, name)
		}

		return true
	})

	return result
}

// =============================================================================
// ENTITY MIXIN DISCOVERY
// =============================================================================

func discoverEntityMixins() (entityMixins, error) {
	result := make(entityMixins)

	entries, err := os.ReadDir(schemaPath)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()

	for _, entry := range entries {
		if entry.IsDir() ||
			!strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		filename := filepath.Join(
			schemaPath,
			entry.Name(),
		)

		file, err := parser.ParseFile(
			fset,
			filename,
			nil,
			0,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"parse entity schema %s: %w",
				filename,
				err,
			)
		}

		entityName := ""

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

				if _, ok := typeSpec.Type.(*ast.StructType); ok {
					entityName = typeSpec.Name.Name
					break
				}
			}
		}

		if entityName == "" {
			continue
		}

		for _, decl := range file.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}

			if funcDecl.Name.Name != "Mixin" ||
				funcDecl.Body == nil {
				continue
			}

			var names []string

			ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
				composite, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}

				selector, ok := composite.Type.(*ast.SelectorExpr)
				if !ok {
					return true
				}

				ident, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}

				if ident.Name == "mixin" {
					names = append(
						names,
						selector.Sel.Name,
					)
				}

				return true
			})

			if len(names) > 0 {
				result[entityName] = names
			}
		}
	}

	return result, nil
}

// =============================================================================
// MIXIN GENERATION
// =============================================================================

func generateMixins(
	mixins map[string]*mixinDefinition,
	registry *enumRegistry,
) error {
	names := make([]string, 0, len(mixins))

	for name := range mixins {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		mixin := mixins[name]

		content, err := renderMixin(
			mixin,
			registry,
		)
		if err != nil {
			return fmt.Errorf(
				"render mixin %s: %w",
				name,
				err,
			)
		}

		filename := filepath.Join(
			mixinDir,
			kebabCase(name)+".ts",
		)

		if err := os.WriteFile(
			filename,
			[]byte(content),
			0o644,
		); err != nil {
			return fmt.Errorf(
				"write mixin %s: %w",
				filename,
				err,
			)
		}
	}

	return nil
}

func renderMixin(
	mixin *mixinDefinition,
	registry *enumRegistry,
) (string, error) {
	var b strings.Builder

	b.WriteString(
		"// Code generated by ent-tsgen. DO NOT EDIT.\n\n",
	)

	imports := make(map[string]string)

	var fields []string

	for _, f := range mixin.Fields {
		if f == nil || f.Name == "" {
			continue
		}

		fields = append(fields, f.Name)
	}

	sort.Strings(fields)

	fieldTypes := make(map[string]string)

	switch mixin.Name {
	case "IDMixin":
		fieldTypes["id"] = "number"

	case "TimeMixin":
		fieldTypes["created_at"] = "string"
		fieldTypes["updated_at"] = "string"

	case "BaseHashMixin":
		fieldTypes["sha256"] = "string"
		fieldTypes["secondary_sha256"] = "string"

	case "JobMixin":
		fieldTypes["state"] = "JobState"
		fieldTypes["status"] = "string"
		fieldTypes["priority"] = "number"
		fieldTypes["attempts"] = "number"
		fieldTypes["max_attempts"] = "number"
		fieldTypes["started_at"] = "string"
		fieldTypes["finished_at"] = "string"
		fieldTypes["error"] = "string"

		imports["JobState"] = "./../enums/job-state"
	}

	fmt.Fprintf(
		&b,
		"export interface %s {\n",
		mixin.Name,
	)

	for _, name := range fields {
		typeName := fieldTypes[name]

		if typeName == "" {
			typeName = "unknown"
		}

		optional := false
		nillable := false

		switch {
		case mixin.Name == "BaseHashMixin" &&
			name == "sha256":
			optional = true
			nillable = true

		case mixin.Name == "BaseHashMixin" &&
			name == "secondary_sha256":
			optional = true

		case mixin.Name == "JobMixin" &&
			(name == "status" ||
				name == "started_at" ||
				name == "finished_at" ||
				name == "error"):
			optional = true
			nillable = true
		}

		if nillable {
			typeName += " | null"
		}

		if optional {
			fmt.Fprintf(
				&b,
				"  %s?: %s;\n",
				name,
				typeName,
			)
		} else {
			fmt.Fprintf(
				&b,
				"  %s: %s;\n",
				name,
				typeName,
			)
		}
	}

	b.WriteString("}\n")

	if len(imports) > 0 {
		body := b.String()

		var header strings.Builder

		names := make([]string, 0, len(imports))
		for name := range imports {
			names = append(names, name)
		}

		sort.Strings(names)

		for _, name := range names {
			fmt.Fprintf(
				&header,
				"import type { %s } from %q;\n",
				name,
				imports[name],
			)
		}

		header.WriteString("\n")
		header.WriteString(body)

		return header.String(), nil
	}

	return b.String(), nil
}

// =============================================================================
// ENTITY GENERATION
// =============================================================================

func generateEntities(
	graph *gen.Graph,
	registry *enumRegistry,
	mixins map[string]*mixinDefinition,
	entityMixinMap entityMixins,
) error {
	nodes := append(
		[]*gen.Type(nil),
		graph.Nodes...,
	)

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})

	for _, node := range nodes {
		content, err := renderEntity(
			node,
			registry,
			mixins,
			entityMixinMap,
		)
		if err != nil {
			return fmt.Errorf(
				"render %s: %w",
				node.Name,
				err,
			)
		}

		filename := filepath.Join(
			outputDir,
			kebabCase(node.Name)+".ts",
		)

		if err := os.WriteFile(
			filename,
			[]byte(content),
			0o644,
		); err != nil {
			return fmt.Errorf(
				"write %s: %w",
				filename,
				err,
			)
		}
	}

	return nil
}

func renderEntity(
	node *gen.Type,
	registry *enumRegistry,
	mixins map[string]*mixinDefinition,
	entityMixinMap entityMixins,
) (string, error) {
	var b strings.Builder

	b.WriteString(
		"// Code generated by ent-tsgen. DO NOT EDIT.\n\n",
	)

	appliedMixins := entityMixinMap[node.Name]

	imports, err := collectEntityImports(
		node,
		registry,
		appliedMixins,
		mixins,
	)
	if err != nil {
		return "", err
	}

	for _, imp := range imports {
		fmt.Fprintf(
			&b,
			"import type { %s } from %q;\n",
			imp.Name,
			imp.Path,
		)
	}

	if len(imports) > 0 {
		b.WriteString("\n")
	}

	fmt.Fprintf(
		&b,
		"export interface %s",
		node.Name,
	)

	validMixins := make([]string, 0, len(appliedMixins))

	for _, mixinName := range appliedMixins {
		if _, ok := mixins[mixinName]; !ok {
			continue
		}

		validMixins = append(
			validMixins,
			mixinName,
		)
	}

	if len(validMixins) > 0 {
		fmt.Fprintf(
			&b,
			" extends %s",
			strings.Join(validMixins, ", "),
		)
	}

	b.WriteString(" {\n")

	mixinFields := collectMixinFieldNames(
		appliedMixins,
		mixins,
	)

	// -------------------------------------------------------------------------
	// Standard Ent ID
	// -------------------------------------------------------------------------
	//
	// Ent provides a standard integer `id` field for every entity unless the
	// entity defines a custom ID field.
	//
	// The implicit Ent ID is not necessarily present in node.Fields, so we
	// explicitly emit it here.
	//
	// If an applied mixin already provides `id` (for example IDMixin), we do
	// not emit it again because the entity inherits it through the mixin.
	//
	// This means both of these cases are valid:
	//
	//   interface User {
	//       id: number;
	//   }
	//
	// and:
	//
	//   interface User extends IDMixin {
	//       ...
	//   }
	//
	// Both expose `id: number` to TypeScript.
	// -------------------------------------------------------------------------

	if !hasField(node.Fields, "id") &&
		!mixinOwnsField("id", appliedMixins, mixins) {
		b.WriteString("  id: number;\n")
	}

	// -------------------------------------------------------------------------
	// Fields
	// -------------------------------------------------------------------------

	for _, f := range node.Fields {
		// Don't duplicate fields provided by mixins.
		if _, ok := mixinFields[f.Name]; ok {
			continue
		}

		typeName, err := fieldType(
			f,
			node,
			registry,
		)
		if err != nil {
			return "", err
		}

		if f.Nillable {
			typeName += " | null"
		}

		if f.Optional {
			fmt.Fprintf(
				&b,
				"  %s?: %s;\n",
				f.Name,
				typeName,
			)
		} else {
			fmt.Fprintf(
				&b,
				"  %s: %s;\n",
				f.Name,
				typeName,
			)
		}
	}

	// -------------------------------------------------------------------------
	// Edges
	// -------------------------------------------------------------------------

	if len(node.Edges) > 0 {
		b.WriteString("\n")
		b.WriteString("  edges: {\n")

		for _, e := range node.Edges {
			if e.Type == nil {
				return "", fmt.Errorf(
					"edge %s.%s has no target type",
					node.Name,
					e.Name,
				)
			}

			if e.Unique {
				fmt.Fprintf(
					&b,
					"    %s?: %s;\n",
					e.Name,
					e.Type.Name,
				)
			} else {
				fmt.Fprintf(
					&b,
					"    %s?: %s[];\n",
					e.Name,
					e.Type.Name,
				)
			}
		}

		b.WriteString("  };\n")
	}

	b.WriteString("}\n")

	return b.String(), nil
}

// =============================================================================
// MIXIN FIELD HELPERS
// =============================================================================

func collectMixinFieldNames(
	mixinNames []string,
	mixins map[string]*mixinDefinition,
) map[string]struct{} {
	result := make(map[string]struct{})

	for _, name := range mixinNames {
		mixin, ok := mixins[name]
		if !ok {
			continue
		}

		for _, f := range mixin.Fields {
			if f == nil {
				continue
			}

			result[f.Name] = struct{}{}
		}
	}

	return result
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
	mixinNames []string,
	mixins map[string]*mixinDefinition,
) bool {
	for _, name := range mixinNames {
		mixin, ok := mixins[name]
		if !ok {
			continue
		}

		for _, f := range mixin.Fields {
			if f != nil && f.Name == fieldName {
				return true
			}
		}
	}

	return false
}

// =============================================================================
// ENTITY IMPORTS
// =============================================================================

func collectEntityImports(
	node *gen.Type,
	registry *enumRegistry,
	appliedMixins []string,
	mixins map[string]*mixinDefinition,
) ([]importSpec, error) {
	imports := make(map[string]string)

	// Entity imports from edges.
	for _, e := range node.Edges {
		if e.Type == nil {
			return nil, fmt.Errorf(
				"edge %s.%s has no target type",
				node.Name,
				e.Name,
			)
		}

		target := e.Type.Name

		if target == node.Name {
			continue
		}

		imports[target] = "./" + kebabCase(target)
	}

	// Enum imports from fields.
	for _, f := range node.Fields {
		if !f.IsEnum() {
			continue
		}

		// If this enum field comes from a mixin, the mixin imports it.
		if mixinOwnsField(
			f.Name,
			appliedMixins,
			mixins,
		) {
			continue
		}

		name := registry.fields[node.Name+"."+f.Name]

		if name == "" {
			return nil, fmt.Errorf(
				"enum registry entry missing for %s.%s",
				node.Name,
				f.Name,
			)
		}

		imports[name] =
			"./enums/" + kebabCase(name)
	}

	// Mixin imports.
	for _, mixinName := range appliedMixins {
		if _, ok := mixins[mixinName]; !ok {
			continue
		}

		imports[mixinName] =
			"./mixins/" + kebabCase(mixinName)
	}

	result := make(
		[]importSpec,
		0,
		len(imports),
	)

	for name, path := range imports {
		result = append(
			result,
			importSpec{
				Name: name,
				Path: path,
			},
		)
	}

	sort.Slice(
		result,
		func(i, j int) bool {
			return result[i].Name < result[j].Name
		},
	)

	return result, nil
}

// =============================================================================
// FIELD TYPES
// =============================================================================

func fieldType(
	f *gen.Field,
	node *gen.Type,
	registry *enumRegistry,
) (string, error) {
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
		return "unknown", nil

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

// =============================================================================
// ENUM FILES
// =============================================================================

func generateEnums(
	registry *enumRegistry,
) error {
	names := make(
		[]string,
		0,
		len(registry.definitions),
	)

	for name := range registry.definitions {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		definition :=
			registry.definitions[name]

		var b strings.Builder

		b.WriteString(
			"// Code generated by ent-tsgen. DO NOT EDIT.\n\n",
		)

		fmt.Fprintf(
			&b,
			"export type %s =\n",
			definition.Name,
		)

		for i, value := range definition.Values {
			if i == len(definition.Values)-1 {
				fmt.Fprintf(
					&b,
					"  | %s;\n",
					tsString(value),
				)
			} else {
				fmt.Fprintf(
					&b,
					"  | %s\n",
					tsString(value),
				)
			}
		}

		filename := filepath.Join(
			enumDir,
			kebabCase(definition.Name)+".ts",
		)

		if err := os.WriteFile(
			filename,
			[]byte(b.String()),
			0o644,
		); err != nil {
			return fmt.Errorf(
				"write enum %s: %w",
				filename,
				err,
			)
		}
	}

	return nil
}

// =============================================================================
// INDEX
// =============================================================================

func generateIndex(
	graph *gen.Graph,
	registry *enumRegistry,
	mixins map[string]*mixinDefinition,
) error {
	var b strings.Builder

	b.WriteString(
		"// Code generated by ent-tsgen. DO NOT EDIT.\n\n",
	)

	// Entities.
	nodes := append(
		[]*gen.Type(nil),
		graph.Nodes...,
	)

	sort.Slice(
		nodes,
		func(i, j int) bool {
			return nodes[i].Name < nodes[j].Name
		},
	)

	for _, node := range nodes {
		fmt.Fprintf(
			&b,
			"export type { %s } from %q;\n",
			node.Name,
			"./"+kebabCase(node.Name),
		)
	}

	// Mixins.
	if len(mixins) > 0 {
		b.WriteString("\n")

		names := make(
			[]string,
			0,
			len(mixins),
		)

		for name := range mixins {
			names = append(names, name)
		}

		sort.Strings(names)

		for _, name := range names {
			fmt.Fprintf(
				&b,
				"export type { %s } from %q;\n",
				name,
				"./mixins/"+kebabCase(name),
			)
		}
	}

	// Enums.
	if len(registry.definitions) > 0 {
		b.WriteString("\n")

		names := make(
			[]string,
			0,
			len(registry.definitions),
		)

		for name := range registry.definitions {
			names = append(
				names,
				name,
			)
		}

		sort.Strings(names)

		for _, name := range names {
			fmt.Fprintf(
				&b,
				"export type { %s } from %q;\n",
				name,
				"./enums/"+kebabCase(name),
			)
		}
	}

	filename := filepath.Join(
		outputDir,
		"index.ts",
	)

	return os.WriteFile(
		filename,
		[]byte(b.String()),
		0o644,
	)
}

// =============================================================================
// HELPERS
// =============================================================================

func lastIdentifier(s string) string {
	s = strings.TrimSpace(s)

	if idx := strings.LastIndex(
		s,
		".",
	); idx >= 0 {
		s = s[idx+1:]
	}

	if idx := strings.LastIndex(
		s,
		"/",
	); idx >= 0 {
		s = s[idx+1:]
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

		if len(runes) > 1 {
			b.WriteString(
				string(runes[1:]),
			)
		}
	}

	return b.String()
}

func kebabCase(s string) string {
	words := splitWords(s)

	for i := range words {
		words[i] =
			strings.ToLower(words[i])
	}

	return strings.Join(
		words,
		"-",
	)
}

func splitWords(s string) []string {
	if s == "" {
		return nil
	}

	var words []string
	var current []rune

	runes := []rune(s)

	flush := func() {
		if len(current) == 0 {
			return
		}

		words = append(
			words,
			string(current),
		)

		current = current[:0]
	}

	for i, r := range runes {
		if r == '_' ||
			r == '-' ||
			r == ' ' ||
			r == '/' {
			flush()
			continue
		}

		if unicode.IsUpper(r) &&
			len(current) > 0 {

			prev := current[len(current)-1]

			nextIsLower :=
				i+1 < len(runes) &&
					unicode.IsLower(
						runes[i+1],
					)

			if unicode.IsLower(prev) ||
				nextIsLower {
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
	a,
	b []string,
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
		`\`,
		`\\`,
	)

	s = strings.ReplaceAll(
		s,
		`"`,
		`\"`,
	)

	s = strings.ReplaceAll(
		s,
		"\n",
		`\n`,
	)

	s = strings.ReplaceAll(
		s,
		"\r",
		`\r`,
	)

	s = strings.ReplaceAll(
		s,
		"\t",
		`\t`,
	)

	return `"` + s + `"`
}
