package hiveclient

import "strings"

// maxFlattenDepth caps recursion through nested struct / array-of-struct
// types. Real Hive schemas rarely exceed 2-3 levels (e.g. Kafka Connect's
// envelope-with-payload), but a corrupted DESCRIBE response with deeply
// nested or self-referential types could otherwise blow the stack. Anything
// beyond this depth is left as-is — caller sees the parent column, not the
// unreachable leaves.
const maxFlattenDepth = 10

// FlattenColumns expands struct- and array-of-struct-typed columns into
// dot-paths so callers can offer leaf-level suggestions. Top-level columns
// always appear in the result; struct leaves are appended with the parent
// name as a prefix. Map types and primitive arrays are kept as-is (no key
// or element enumeration is possible from the schema alone).
//
// Example input:
//
//	[{Name: "kubernetes", Type: "struct<pod:string,ns:string>"},
//	 {Name: "log",        Type: "string"}]
//
// Result:
//
//	[{Name: "kubernetes",     Type: "struct<...>"},
//	 {Name: "kubernetes.pod", Type: "string"},
//	 {Name: "kubernetes.ns",  Type: "string"},
//	 {Name: "log",            Type: "string"}]
//
// Malformed type strings (unbalanced brackets etc.) fall back to the column
// as-is — no crash, no partial expansion.
func FlattenColumns(cols []ColumnSpec) []ColumnSpec {
	out := make([]ColumnSpec, 0, len(cols))
	for _, c := range cols {
		out = append(out, c)
		for _, leaf := range expandType(c.Name, c.Type, 0) {
			leaf.IsPartition = c.IsPartition
			out = append(out, leaf)
		}
	}
	return out
}

// expandType returns the flattened child paths for a single column type.
// Top-level call: parentName = column name, raw = column type string, depth = 0.
// Recursive call: parentName = "parent.child", raw = child's type, depth = parentDepth+1.
// Recursion stops at maxFlattenDepth — see the constant's doc for rationale.
func expandType(parentName, raw string, depth int) []ColumnSpec {
	if depth >= maxFlattenDepth {
		return nil
	}
	t := strings.TrimSpace(raw)
	lower := strings.ToLower(t)

	// struct<...> — recurse into each named field.
	if inner, ok := stripWrapper(lower, t, "struct"); ok {
		fields := splitTopLevel(inner, ',')
		out := make([]ColumnSpec, 0, len(fields))
		for _, f := range fields {
			name, ftype, ok := strings.Cut(f, ":")
			if !ok {
				continue
			}
			name = strings.TrimSpace(name)
			ftype = strings.TrimSpace(ftype)
			if name == "" {
				continue
			}
			full := parentName + "." + name
			out = append(out, ColumnSpec{Name: full, Type: ftype})
			out = append(out, expandType(full, ftype, depth+1)...)
		}
		return out
	}

	// array<struct<...>> — treat as a struct from the autocomplete perspective.
	// (Hive lets you reference array<struct<x>>.x in some contexts; even if a
	// particular customer query needs LATERAL VIEW EXPLODE, suggesting the
	// field paths is still useful.) Primitive arrays are left untouched.
	if inner, ok := stripWrapper(lower, t, "array"); ok {
		innerLower := strings.ToLower(strings.TrimSpace(inner))
		if strings.HasPrefix(innerLower, "struct<") {
			// array<struct<...>> stays at the same depth — the array wrapper
			// doesn't introduce a new dot-path level.
			return expandType(parentName, inner, depth)
		}
		return nil
	}

	// map<k,v> — no key enumeration possible from schema; ignore.
	if _, ok := stripWrapper(lower, t, "map"); ok {
		return nil
	}

	// Primitive or unknown — nothing to expand.
	return nil
}

// stripWrapper returns the contents inside <...> for a complex type with the
// given prefix (e.g. "struct"). It checks lower for the prefix to be
// case-insensitive but returns the substring from the original (non-lowered)
// string so embedded identifiers keep their case.
func stripWrapper(lower, original, prefix string) (string, bool) {
	if !strings.HasPrefix(lower, prefix+"<") {
		return "", false
	}
	if !strings.HasSuffix(lower, ">") {
		return "", false
	}
	// Use original to preserve identifier casing.
	return original[len(prefix)+1 : len(original)-1], true
}

// splitTopLevel splits s by sep, but only at bracket depth zero so nested
// struct/array/map types stay intact.
func splitTopLevel(s string, sep byte) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	if start <= len(s) {
		out = append(out, s[start:])
	}
	return out
}
