# entToTsxTypes

Generate TypeScript types from an [Ent](https://entgo.io/) schema for use in a TypeScript / React / Next.js frontend.

`entToTsxTypes` reads your Ent schema directly from Go and generates:

* TypeScript entity interfaces
* TypeScript enum types
* TypeScript mixin interfaces
* Entity relationships / edges
* Proper TypeScript imports between generated types
* A central `index.ts` barrel file

The goal is to keep your **Go/Ent backend types and frontend TypeScript types synchronized automatically**, without manually maintaining duplicate interfaces.

---

## Features

### Ent entities → TypeScript interfaces

An Ent schema such as:

```go
type User struct {
	ent.Schema
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id"),
		field.String("name"),
		field.String("email").Optional(),
	}
}
```

can be represented as:

```ts
export interface User {
  id: number;
  name: string;
  email?: string;
}
```

---

### Ent enums → TypeScript union types

Ent enums are converted into TypeScript string unions.

For example:

```go
field.Enum("state").
	Values(
		"pending",
		"running",
		"finished",
		"failed",
	)
```

generates:

```ts
export type JobState =
  | "pending"
  | "running"
  | "finished"
  | "failed";
```

Enums are generated into:

```text
frontend/types/enums/
```

---

### Ent edges → TypeScript relationships

Ent edges are represented inside an `edges` property.

A unique edge:

```go
edge.To("owner", User.Type)
```

becomes:

```ts
edges: {
  owner?: User;
};
```

A non-unique edge:

```go
edge.To("messages", Message.Type)
```

becomes:

```ts
edges: {
  messages?: Message[];
};
```

Entity types are automatically imported when required.

---

### Ent mixins → TypeScript interfaces

The generator also detects Ent mixins and generates corresponding TypeScript interfaces.

For example:

```go
type IDMixin struct {
	mixin.Schema
}

func (IDMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id"),
	}
}
```

generates:

```ts
export interface IDMixin {
  id: number;
}
```

Entities using the mixin can then extend the generated interface:

```ts
export interface User extends IDMixin {
  name: string;
}
```

This prevents fields supplied by mixins from being duplicated in the entity interface.

---

## Generated directory structure

By default, the generator writes to:

```text
frontend/types/
├── index.ts
├── user.ts
├── message.ts
├── chat.ts
├── enums/
│   ├── job-state.ts
│   └── message-type.ts
└── mixins/
    ├── id-mixin.ts
    ├── time-mixin.ts
    └── job-mixin.ts
```

The generated `index.ts` re-exports all entities, mixins and enums:

```ts
export type { User } from "./user";
export type { Message } from "./message";

export type { IDMixin } from "./mixins/id-mixin";

export type { JobState } from "./enums/job-state";
```

This allows frontend code to simply use:

```ts
import type {
  User,
  Message,
  JobState,
} from "@/types";
```

---

# Installation

`entToTsxTypes` is a Go program and is intended to live inside the Go project containing your Ent schema.

The generator uses Ent's code-generation packages:

```bash
go get entgo.io/ent
```

If your project already uses Ent, these dependencies should already be available.

---

# Project structure

The generator expects an Ent project with a structure similar to:

```text
project/
├── ent/
│   ├── generate.go
│   ├── schema/
│   │   ├── user.go
│   │   ├── message.go
│   │   ├── chat.go
│   │   └── mixin/
│   │       ├── id.go
│   │       ├── time.go
│   │       └── job.go
│   └── ...
│
├── frontend/
│   └── types/
│
└── entToTsxTypes/
    └── main.go
```

The important paths are:

```text
../ent/schema
../frontend/types
../frontend/types/enums
../frontend/types/mixins
```

These paths are relative to the directory from which the generator is executed.

---

# Configuration

The paths are currently configured at the top of `main.go`:

```go
const (
	// go:generate runs from the ent/ directory.
	schemaPath = "../ent/schema"
	outputDir  = "../frontend/types"
	enumDir    = "../frontend/types/enums"
	mixinDir   = "../frontend/types/mixins"
)
```

Adjust these paths to match your project structure.

> **Important:** `schemaPath` is currently relative to the working directory used when running the generator.

---

# Usage

## Option 1 — Run directly

From the appropriate project directory:

```bash
go run ./path/to/entToTsxTypes
```

The generator will:

1. Load the Ent graph.
2. Discover enums.
3. Discover Ent mixins.
4. Discover which entities use which mixins.
5. Remove the previous generated output.
6. Generate mixin interfaces.
7. Generate entity interfaces.
8. Generate enum types.
9. Generate the `index.ts` barrel file.

If generation succeeds, the output will be available under:

```text
frontend/types/
```

---

# Using `go:generate`

The generator is designed to work particularly well with Go's `go:generate`.

For example, if generation should be triggered from your `ent/` directory:

```go
//go:generate go run ../entToTsxTypes
```

You can then run:

```bash
go generate ./ent
```

This regenerates the TypeScript types from the current Ent schema.

---

# Recommended workflow

A typical workflow is:

```text
Ent schema
    │
    ▼
Go / Ent
    │
    ▼
entToTsxTypes
    │
    ├── entities
    ├── enums
    └── mixins
    │
    ▼
frontend/types
    │
    ▼
Next.js / React / TypeScript
```

Whenever the Ent schema changes, regenerate the TypeScript types:

```bash
go generate ./ent
```

Then commit the generated files if your project tracks generated frontend types.

---

# Type mappings

The generator maps Ent field types to TypeScript types.

| Ent type  | TypeScript                        |
| --------- | --------------------------------- |
| `Bool`    | `boolean`                         |
| `String`  | `string`                          |
| `Time`    | `string`                          |
| `UUID`    | `string`                          |
| `Bytes`   | `string`                          |
| `Int`     | `number`                          |
| `Int8`    | `number`                          |
| `Int16`   | `number`                          |
| `Int32`   | `number`                          |
| `Int64`   | `number`                          |
| `Uint`    | `number`                          |
| `Uint8`   | `number`                          |
| `Uint16`  | `number`                          |
| `Uint32`  | `number`                          |
| `Uint64`  | `number`                          |
| `Float32` | `number`                          |
| `Float64` | `number`                          |
| `JSON`    | `unknown`                         |
| `Enum`    | generated union type              |
| `Other`   | Go type identifier when available |

---

# Optional and nullable fields

Ent's optional and nillable properties are represented separately.

An optional field:

```go
field.String("name").Optional()
```

becomes:

```ts
name?: string;
```

A nillable field:

```go
field.String("name").Nillable()
```

becomes:

```ts
name: string | null;
```

An optional + nillable field:

```go
field.String("name").
	Optional().
	Nillable()
```

becomes:

```ts
name?: string | null;
```

---

# Custom Go enums

The generator also supports Ent enums backed by a custom Go type.

For example:

```go
field.Enum("state").
	Values(
		"pending",
		"running",
		"finished",
	).
	GoType(JobState(""))
```

The generator uses the custom Go type name:

```ts
export type JobState =
  | "pending"
  | "running"
  | "finished";
```

This makes it possible for multiple Ent fields to share the same logical enum type.

---

# Mixins

Mixins are detected from the Ent schema source.

The generator recognizes structs embedding:

```go
mixin.Schema
```

and reads their `Fields()` definitions.

Entities using mixins are detected from their `Mixin()` method.

For example:

```go
func (User) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.IDMixin{},
		mixin.TimeMixin{},
	}
}
```

will cause the generated entity to extend those interfaces:

```ts
export interface User extends IDMixin, TimeMixin {
  ...
}
```

Fields supplied by the mixins are not emitted again inside the entity interface.

---

# Built-in mixin mappings

The generator currently contains explicit mappings for several common mixins.

## `IDMixin`

```ts
export interface IDMixin {
  id: number;
}
```

## `TimeMixin`

```ts
export interface TimeMixin {
  created_at: string;
  updated_at: string;
}
```

## `BaseHashMixin`

```ts
export interface BaseHashMixin {
  secondary_sha256?: string;
  sha256?: string | null;
}
```

## `JobMixin`

```ts
export interface JobMixin {
  attempts: number;
  error?: string | null;
  finished_at?: string | null;
  max_attempts: number;
  priority: number;
  started_at?: string | null;
  state: JobState;
  status?: string | null;
}
```

The `JobState` enum is automatically imported by the generated mixin.

---

# Generated files are not meant to be edited

Every generated file contains:

```ts
// Code generated by ent-tsgen. DO NOT EDIT.
```

Do not manually modify generated files.

Instead, change the Ent schema and regenerate:

```bash
go generate ./ent
```

---

# Error handling

The generator exits with a non-zero status if generation fails.

Errors are prefixed with:

```text
ent-tsgen:
```

For example:

```text
ent-tsgen: load Ent graph: ...
```

Enum conflicts are also detected.

If two fields resolve to the same TypeScript enum name but contain different values, generation fails instead of silently producing an incorrect type.

For example:

```text
enum "JobState" has conflicting values between fields
```

This is intentional: silently merging incompatible enum definitions could create invalid frontend types.

---

# Important limitations

This project intentionally generates **TypeScript type definitions**, not a complete Go → TypeScript schema compiler.

Some mappings are therefore conservative.

### JSON

Ent JSON fields currently become:

```ts
unknown
```

You can manually provide more specific frontend types where required.

### Bytes

Ent `Bytes` fields currently become:

```ts
string
```

This assumes the backend representation exposed to the frontend is a string-compatible representation.

### Unknown / custom fields

Unknown field types become:

```ts
unknown
```

unless Ent exposes a usable Go type identifier.

### Mixins

Mixin field type resolution currently contains explicit mappings for known mixins such as:

* `IDMixin`
* `TimeMixin`
* `BaseHashMixin`
* `JobMixin`

Unknown mixin fields may therefore be generated as:

```ts
unknown
```

This is an area that can be extended as the project evolves.

---

# Why use this?

When a Go backend and TypeScript frontend share the same data model, manually maintaining both sides quickly becomes painful.

Without generation:

```text
Go Ent schema
     │
     ├── change field
     │
     ├── update Go code
     │
     ├── update TypeScript interface
     │
     └── hope nothing was missed
```

With `entToTsxTypes`:

```text
Go Ent schema
     │
     ▼
entToTsxTypes
     │
     ▼
TypeScript types
```

The Ent schema becomes the source of truth.

---

# Requirements

* Go
* Ent
* A working Ent schema
* TypeScript / frontend project if consuming the generated types

The generator itself does not require Node.js.

---

# License

Add the license you want to use for this project here.

For example:

```text
MIT License
```

---

# Contributing

Contributions are welcome.

Some useful areas for improvement include:

* Better automatic mixin type inference
* More complete Ent → TypeScript type mappings
* Configurable input/output paths
* CLI flags
* JSON type customization
* Support for additional custom Go types
* Better handling of generated Ent schemas
* Configurable naming conventions

If you find an issue, please open an issue with:

1. The relevant Ent schema.
2. The generated TypeScript output.
3. The expected output.
4. The generator error, if applicable.

---

## Example

Given an Ent model:

```go
type Message struct {
	ent.Schema
}

func (Message) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id"),
		field.String("body"),
		field.Enum("type").
			Values(
				"text",
				"image",
				"audio",
			),
		field.Time("created_at"),
	}
}
```

the generator can produce:

```ts
import type { MessageType } from "./enums/message-type";

export interface Message {
  id: number;
  body: string;
  type: MessageType;
  created_at: string;
}
```

with the enum:

```ts
export type MessageType =
  | "text"
  | "image"
  | "audio";
```

and expose everything through:

```ts
import type {
  Message,
  MessageType,
} from "@/types";
```

---

## Summary

`entToTsxTypes` is a small Go-based generator that bridges **Ent schemas and TypeScript frontend types**.

It is designed to keep the backend and frontend data models synchronized while remaining simple enough to drop into an existing Go + Ent + TypeScript project.

```text
Ent Schema
    ↓
entToTsxTypes
    ↓
TypeScript entities
TypeScript enums
TypeScript mixins
TypeScript edges
    ↓
Next.js / React frontend
```
