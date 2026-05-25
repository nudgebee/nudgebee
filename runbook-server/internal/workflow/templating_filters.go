package workflow

import (
	"bytes"
	"crypto/md5"
	crypto_rand "crypto/rand" // For secure random numbers like MAC addresses
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"math/rand"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/google/uuid"
	"github.com/ncruces/go-strftime"
	"github.com/nikolalohinski/gonja/v2"
	"github.com/nikolalohinski/gonja/v2/exec"
	"gopkg.in/yaml.v3"
)

func init() {
	registerEncodingFilters()
	registerPathFilters()
	registerDataFilters()
	registerListLogicFilters()
	registerRegexFilters()
	registerTimeFilters()
	registerMiscFilters()
	registerDictFilters()
	registerUrlFilters()
	registerHumanFilters()
	registerMathFilters()
	registerFixFilters()
}

// sanitize converts gonja internal types (like Dicts) to native Go types.
// Safe version using gonja's public API where possible.
func sanitize(val any) any {
	if val == nil {
		return nil
	}

	// Direct handling of *exec.Value using Iterate method which is safe
	if v, ok := val.(*exec.Value); ok {
		if v.IsDict() {
			out := make(map[string]any)
			// Providing empty fallback to avoid potential nil pointer dereference in gonja
			v.Iterate(func(idx, count int, key, value *exec.Value) bool {
				if key != nil {
					out[key.String()] = sanitize(value) // Recursively sanitize
				}
				return true
			}, func() {})
			return out
		}
		if v.IsList() {
			var out []any
			v.Iterate(func(idx, count int, item, _ *exec.Value) bool {
				out = append(out, sanitize(item))
				return true
			}, func() {})
			return out
		}
		// If not Dict or List, unwrap
		return sanitize(v.Interface())
	}

	// Reflection fallback for other types
	v := reflect.ValueOf(val)
	// Dereference pointers/interfaces loop
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	if !v.IsValid() {
		return nil
	}

	switch v.Kind() {
	// If it's a Map, convert keys to string if needed and recursive sanitize values
	case reflect.Map:
		out := make(map[string]any)
		iter := v.MapRange()
		for iter.Next() {
			k := iter.Key()
			val := iter.Value()
			var kVal, vVal any
			if k.CanInterface() {
				kVal = k.Interface()
			} else {
				kVal = k.String()
			}

			if val.CanInterface() {
				vVal = sanitize(val.Interface())
			}
			out[fmt.Sprintf("%v", kVal)] = vVal
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			if v.Index(i).CanInterface() {
				out[i] = sanitize(v.Index(i).Interface())
			}
		}
		return out
	case reflect.Struct:
		if v.CanInterface() {
			return v.Interface()
		}
		return nil
	default:
		if v.CanInterface() {
			return v.Interface()
		}
		return nil
	}
}

func registerEncodingFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("b64encode", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		return exec.AsValue(base64.StdEncoding.EncodeToString([]byte(in.String())))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("b64decode", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		decoded, err := base64.StdEncoding.DecodeString(in.String())
		if err != nil {
			panic(err)
		}
		return exec.AsValue(string(decoded))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("md5", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		hash := md5.Sum([]byte(in.String()))
		return exec.AsValue(hex.EncodeToString(hash[:]))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("sha1", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		hash := sha1.Sum([]byte(in.String()))
		return exec.AsValue(hex.EncodeToString(hash[:]))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("checksum", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Default to sha1 per Ansible
		hash := sha1.Sum([]byte(in.String()))
		return exec.AsValue(hex.EncodeToString(hash[:]))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("hash", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		s := in.String()
		algo := "sha1"
		if len(params.Args) > 0 {
			algo = params.Args[0].String()
		}
		switch algo {
		case "md5":
			h := md5.Sum([]byte(s))
			return exec.AsValue(hex.EncodeToString(h[:]))
		case "sha1":
			h := sha1.Sum([]byte(s))
			return exec.AsValue(hex.EncodeToString(h[:]))
		case "crc32":
			crc := crc32.ChecksumIEEE([]byte(s))
			return exec.AsValue(fmt.Sprintf("%x", crc))
		default:
			return exec.AsValue("") // Unknown algo
		}
	})
	if err != nil {
		panic(err)
	}
}

func registerPathFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("basename", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		return exec.AsValue(filepath.Base(in.String()))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("dirname", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		return exec.AsValue(filepath.Dir(in.String()))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("splitext", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		path := in.String()
		ext := filepath.Ext(path)
		root := strings.TrimSuffix(path, ext)
		return exec.AsValue([]string{root, ext})
	})
	if err != nil {
		panic(err)
	}

	// err = gonja.DefaultEnvironment.Filters.Register("realpath", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// 	path, err := filepath.Abs(in.String())
	// 	if err != nil {
	// 		return in
	// 	}
	// 	return exec.AsValue(path)
	// })
	// if err != nil {
	// 	panic(err)
	// }

	// err = gonja.DefaultEnvironment.Filters.Register("relpath", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// 	base, getwdErr := os.Getwd()
	// 	if getwdErr != nil {
	// 		panic(getwdErr)
	// 	}
	// 	if len(params.Args) > 0 {
	// 		base = params.Args[0].String()
	// 	}
	// 	rel, relErr := filepath.Rel(base, in.String())
	// 	if relErr != nil {
	// 		panic(relErr)
	// 	}
	// 	return exec.AsValue(rel)
	// })
	// if err != nil {
	// 	panic(err)
	// }

	// err = gonja.DefaultEnvironment.Filters.Register("expandvars", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// 	return exec.AsValue(os.ExpandEnv(in.String()))
	// })
	// if err != nil {
	// 	panic(err)
	// }

	// err = gonja.DefaultEnvironment.Filters.Register("expanduser", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// 	path := in.String()
	// 	if strings.HasPrefix(path, "~") {
	// 		usr, err := user.Current()
	// 		if err == nil {
	// 			if path == "~" {
	// 				return exec.AsValue(usr.HomeDir)
	// 			}
	// 			return exec.AsValue(filepath.Join(usr.HomeDir, path[2:]))
	// 		}
	// 	}
	// 	return exec.AsValue(path)
	// })
	// if err != nil {
	// 	panic(err)
	// }

	// err = gonja.DefaultEnvironment.Filters.Register("fileglob", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// 	matches, err := filepath.Glob(in.String())
	// 	if err != nil {
	// 		return exec.AsValue([]string{})
	// 	}
	// 	return exec.AsValue(matches)
	// })
	// if err != nil {
	// 	panic(err)
	// }

	// Windows paths (simulated with string manipulation to work on linux too if needed, or just standard path logic)
	// Since runbook-server runs on Linux containers usually, these might just use filepath (which is OS aware).
	// But Ansible win_* filters are expected to handle Windows paths even if running on Linux controller.
	// We will implement basic string manipulation for win_* filters.
	err = gonja.DefaultEnvironment.Filters.Register("win_basename", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		path := in.String()
		// Split by \
		parts := strings.Split(path, "\\")
		if len(parts) > 0 {
			return exec.AsValue(parts[len(parts)-1])
		}
		return exec.AsValue("")
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("win_dirname", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		path := in.String()
		parts := strings.Split(path, "\\")
		if len(parts) > 1 {
			return exec.AsValue(strings.Join(parts[:len(parts)-1], "\\"))
		}
		return exec.AsValue("")
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("win_splitdrive", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		path := in.String()
		// C:\path -> C:, \path
		if len(path) >= 2 && path[1] == ':' {
			return exec.AsValue([]string{path[:2], path[2:]})
		}
		return exec.AsValue([]string{"", path})
	})
	if err != nil {
		panic(err)
	}

	// New Path Utils
	err = gonja.DefaultEnvironment.Filters.Register("path_join", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// List of paths or multiple args?
		// Ansible: list | path_join
		val := sanitize(in)
		var parts []string
		v := reflect.ValueOf(val)
		if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			for i := 0; i < v.Len(); i++ {
				parts = append(parts, fmt.Sprintf("%v", v.Index(i).Interface()))
			}
		} else {
			parts = append(parts, in.String())
		}
		return exec.AsValue(filepath.Join(parts...))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("commonpath", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// List of paths
		val := sanitize(in)
		var paths []string
		v := reflect.ValueOf(val)
		if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			for i := 0; i < v.Len(); i++ {
				paths = append(paths, fmt.Sprintf("%v", v.Index(i).Interface()))
			}
		}

		// Simple implementation finding common prefix
		if len(paths) == 0 {
			return exec.AsValue("")
		}
		sep := string(filepath.Separator)
		// Clean all paths
		for i, p := range paths {
			paths[i] = filepath.Clean(p)
		}

		c := paths[0]
		for _, p := range paths[1:] {
			for !strings.HasPrefix(p, c) {
				if c == "." || c == sep || c == "" {
					return exec.AsValue("")
				}
				c = filepath.Dir(c)
			}
		}
		return exec.AsValue(c)
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("normpath", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		return exec.AsValue(filepath.Clean(in.String()))
	})
	if err != nil {
		panic(err)
	}
}

func registerDataFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("from_json", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		var out any
		err := json.Unmarshal([]byte(in.String()), &out)
		if err != nil {
			return exec.AsValue(nil)
		}
		return exec.AsValue(out)
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("to_nice_json", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Use sanitize on the value directly (likely *exec.Value from template)
		// If in is *exec.Value, sanitize will use Iterate()
		obj := sanitize(in)

		indent := 4
		if len(params.Args) > 0 {
			indent = params.Args[0].Integer()
		}
		indentStr := strings.Repeat(" ", indent)
		b, err := json.MarshalIndent(obj, "", indentStr)
		if err != nil {
			return exec.AsValue("")
		}
		return exec.AsValue(string(b))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("to_json", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		obj := sanitize(in)
		b, err := json.Marshal(obj)
		if err != nil {
			return exec.AsValue("")
		}
		return exec.AsValue(string(b))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("from_yaml", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		var out any
		err := yaml.Unmarshal([]byte(in.String()), &out)
		if err != nil {
			return exec.AsValue(nil)
		}
		return exec.AsValue(out)
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("from_yaml_all", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Parse YAML stream using yaml.Decoder
		decoder := yaml.NewDecoder(bytes.NewBufferString(in.String()))
		var results []any
		for {
			var out any
			if err := decoder.Decode(&out); err != nil {
				break
			}
			results = append(results, out)
		}
		return exec.AsValue(results)
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("to_yaml", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		obj := sanitize(in)
		b, err := yaml.Marshal(obj)
		if err != nil {
			return exec.AsValue("")
		}
		return exec.AsValue(string(b))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("to_nice_yaml", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// yaml.Marshal is already "nice" (indented), but ansible allows configuring indent.
		// gopkg.in/yaml.v3 basic Marshal defaults to 4 spaces usually (or 2).
		// We can stick to standard Marshal for now as configuring indent in yaml.v3 requires Encoder.
		obj := sanitize(in)
		var b bytes.Buffer
		enc := yaml.NewEncoder(&b)
		indent := 2
		if len(params.Args) > 0 {
			indent = params.Args[0].Integer()
		}
		enc.SetIndent(indent)
		err := enc.Encode(obj)
		if err != nil {
			return exec.AsValue("")
		}
		return exec.AsValue(b.String())
	})
	if err != nil {
		panic(err)
	}
}

func registerListLogicFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("flatten", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Basic flattening 1 level or recursive? Ansible `flatten(levels=None)` recursive by default.
		// gonja input likely slice of slice
		val := sanitize(in)
		// Recursive flatten helper
		var flat []any
		var flatten func(any)
		flatten = func(v any) {
			r := reflect.ValueOf(v)
			// Unwrap if needed (sanitize already does, but just in case of weird nesting)
			for r.Kind() == reflect.Ptr || r.Kind() == reflect.Interface {
				if r.IsNil() {
					return
				}
				r = r.Elem()
			}

			if r.Kind() == reflect.Slice || r.Kind() == reflect.Array {
				for i := 0; i < r.Len(); i++ {
					flatten(r.Index(i).Interface())
				}
			} else {
				flat = append(flat, v)
			}
		}
		flatten(val)
		return exec.AsValue(flat)
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("shuffle", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		val := sanitize(in)
		v := reflect.ValueOf(val)
		if v.Kind() == reflect.Slice {
			// Make a copy
			shuffled := make([]any, v.Len())
			for i := 0; i < v.Len(); i++ {
				shuffled[i] = v.Index(i).Interface()
			}
			rand.Shuffle(len(shuffled), func(i, j int) {
				shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
			})
			return exec.AsValue(shuffled)
		}
		return in
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("ternary", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// val | ternary(true_val, false_val)
		truthy := in.IsTrue()
		trueVal := params.Args[0]
		falseVal := params.Args[1]
		if truthy {
			return trueVal
		}
		return falseVal
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("mandatory", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		if in.IsNil() || (in.IsString() && in.String() == "") {
			msg := "Mandatory variable undefined"
			if len(params.Args) > 0 {
				msg = params.Args[0].String()
			}
			panic(errors.New(msg))
		}
		return in
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("ans_random", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Input could be limit (int) or start/end/step.
		// Or "list" | random -> random item
		// If input is list, pick random item.
		val := sanitize(in)
		if val == nil {
			return exec.AsValue(nil)
		}

		v := reflect.ValueOf(val)

		// Dereference if needed
		for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return exec.AsValue(nil)
			}
			v = v.Elem()
		}

		// Handle integer (random range 0..N)
		switch v.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			limit := v.Int()
			if limit <= 0 {
				return exec.AsValue(0)
			}
			return exec.AsValue(rand.Intn(int(limit)))
		case reflect.Float32, reflect.Float64:
			// handle float? cast to int
			limit := int(v.Float())
			if limit <= 0 {
				return exec.AsValue(0)
			}
			return exec.AsValue(rand.Intn(limit))
		}

		// Handle list
		if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			if v.Len() == 0 {
				return exec.AsValue(nil)
			}
			idx := rand.Intn(v.Len())
			return exec.AsValue(v.Index(idx).Interface())
		}

		// Fallback return input if not supported type
		return in
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("ans_groupby", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Input: list of dicts. Arg: key to group by.
		// Returns list of tuples (key, list_of_items)
		// Minimal implementation
		key := params.Args[0].String()
		val := sanitize(in)
		v := reflect.ValueOf(val)

		groups := make(map[string][]any)
		var keys []string

		if v.Kind() == reflect.Slice {
			for i := 0; i < v.Len(); i++ {
				item := v.Index(i).Interface()
				// item is any from sanitize, likely map[string]any
				itemMap, ok := item.(map[string]any)
				if !ok {
					continue
				}
				groupKey := fmt.Sprintf("%v", itemMap[key])
				if _, exists := groups[groupKey]; !exists {
					keys = append(keys, groupKey)
				}
				groups[groupKey] = append(groups[groupKey], item)
			}
		}

		sort.Strings(keys)
		var result []any // list of [key, list]
		for _, k := range keys {
			result = append(result, []any{k, groups[k]})
		}
		return exec.AsValue(result)
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("extract", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// list | extract(container) -> maps list items to values in container
		// e.g. ['a', 'b'] | extract({'a': 1, 'b': 2}) -> [1, 2]
		if len(params.Args) == 0 {
			return exec.AsValue(nil)
		}
		container := sanitize(params.Args[0])
		keys := sanitize(in)

		containerVal := reflect.ValueOf(container)
		keysVal := reflect.ValueOf(keys)

		if keysVal.Kind() == reflect.Slice {
			var res []any
			for i := 0; i < keysVal.Len(); i++ {
				k := fmt.Sprintf("%v", keysVal.Index(i).Interface())
				if containerVal.Kind() == reflect.Map {
					v := containerVal.MapIndex(reflect.ValueOf(k))
					if v.IsValid() {
						res = append(res, v.Interface())
					} else {
						res = append(res, nil)
					}
				}
			}
			return exec.AsValue(res)
		}
		k := fmt.Sprintf("%v", keys)
		if containerVal.Kind() == reflect.Map {
			v := containerVal.MapIndex(reflect.ValueOf(k))
			if v.IsValid() {
				return exec.AsValue(v.Interface())
			}
		}
		return exec.AsValue(nil)
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("subelements", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Ansible subelements filter.
		// list | subelements('sublist_key')
		// Returns list of [parent_item, subitem]
		if len(params.Args) == 0 {
			return exec.AsValue(nil)
		}
		key := params.Args[0].String()
		list := sanitize(in)
		v := reflect.ValueOf(list)

		var res []any

		if v.Kind() == reflect.Slice {
			for i := 0; i < v.Len(); i++ {
				item := v.Index(i).Interface()
				itemMap, ok := item.(map[string]any)
				if !ok {
					continue
				}
				sub, ok := itemMap[key]
				if !ok {
					continue // skip or error? Ansible skips if skip_missing=True
				}

				subVal := reflect.ValueOf(sub)
				if subVal.Kind() == reflect.Slice {
					for j := 0; j < subVal.Len(); j++ {
						res = append(res, []any{item, subVal.Index(j).Interface()})
					}
				}
			}
		}
		return exec.AsValue(res)
	})
	if err != nil {
		panic(err)
	}

	// New List Set Theory Filters
	setOp := func(in *exec.Value, args []*exec.Value, op string) *exec.Value {
		val1 := sanitize(in)
		val2 := sanitize(args[0])

		v1 := reflect.ValueOf(val1) // Original slice to preserve types if possible? simpler to stringify for set logic
		// But for output we might want original types.
		// For simplicity, we return list of strings or list of whatever was in input if unique.
		// Let's stick to string comparison for "set" logic, but try to return original values if they are simple.

		// Better approach: maintain order and uniqueness.

		list1 := []any{}
		if v1.Kind() == reflect.Slice || v1.Kind() == reflect.Array {
			for i := 0; i < v1.Len(); i++ {
				list1 = append(list1, v1.Index(i).Interface())
			}
		}

		list2 := []any{}
		v2 := reflect.ValueOf(val2)
		if v2.Kind() == reflect.Slice || v2.Kind() == reflect.Array {
			for i := 0; i < v2.Len(); i++ {
				list2 = append(list2, v2.Index(i).Interface())
			}
		}

		result := []any{}
		exists := make(map[string]bool)

		switch op {
		case "union":
			// A | union(B) -> all unique items
			for _, item := range list1 {
				k := fmt.Sprintf("%v", item)
				if !exists[k] {
					exists[k] = true
					result = append(result, item)
				}
			}
			for _, item := range list2 {
				k := fmt.Sprintf("%v", item)
				if !exists[k] {
					exists[k] = true
					result = append(result, item)
				}
			}
		case "intersect":
			// A | intersect(B) -> items in both
			set2 := make(map[string]bool)
			for _, item := range list2 {
				set2[fmt.Sprintf("%v", item)] = true
			}
			for _, item := range list1 {
				k := fmt.Sprintf("%v", item)
				if set2[k] && !exists[k] {
					exists[k] = true
					result = append(result, item)
				}
			}
		case "difference":
			// A | difference(B) -> items in A but not B
			set2 := make(map[string]bool)
			for _, item := range list2 {
				set2[fmt.Sprintf("%v", item)] = true
			}
			for _, item := range list1 {
				k := fmt.Sprintf("%v", item)
				if !set2[k] && !exists[k] {
					exists[k] = true
					result = append(result, item)
				}
			}
		case "symmetric_difference":
			// (A - B) + (B - A)
			set1 := make(map[string]bool)
			for _, item := range list1 {
				set1[fmt.Sprintf("%v", item)] = true
			}
			set2 := make(map[string]bool)
			for _, item := range list2 {
				set2[fmt.Sprintf("%v", item)] = true
			}

			for _, item := range list1 {
				k := fmt.Sprintf("%v", item)
				if !set2[k] && !exists[k] {
					exists[k] = true
					result = append(result, item)
				}
			}
			for _, item := range list2 {
				k := fmt.Sprintf("%v", item)
				if !set1[k] && !exists[k] {
					exists[k] = true
					result = append(result, item)
				}
			}
		}

		return exec.AsValue(result)
	}

	err = gonja.DefaultEnvironment.Filters.Register("union", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		if len(params.Args) == 0 {
			return in
		}
		return setOp(in, params.Args, "union")
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("intersect", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		if len(params.Args) == 0 {
			return exec.AsValue([]any{})
		}
		return setOp(in, params.Args, "intersect")
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("difference", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		if len(params.Args) == 0 {
			return in
		}
		return setOp(in, params.Args, "difference")
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("symmetric_difference", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		if len(params.Args) == 0 {
			return in
		}
		return setOp(in, params.Args, "symmetric_difference")
	})
	if err != nil {
		panic(err)
	}
}

func registerRegexFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("regex_search", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		pattern := params.Args[0].String()
		re, err := regexp.Compile(pattern)
		if err != nil {
			return exec.AsValue(nil)
		}
		return exec.AsValue(re.FindString(in.String()))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("regex_findall", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		pattern := params.Args[0].String()
		re, err := regexp.Compile(pattern)
		if err != nil {
			return exec.AsValue([]string{})
		}
		return exec.AsValue(re.FindAllString(in.String(), -1))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("regex_replace", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		pattern := params.Args[0].String()
		repl := params.Args[1].String()
		re, err := regexp.Compile(pattern)
		if err != nil {
			return in
		}
		return exec.AsValue(re.ReplaceAllString(in.String(), repl))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("regex_escape", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		return exec.AsValue(regexp.QuoteMeta(in.String()))
	})
	if err != nil {
		panic(err)
	}
}

func registerTimeFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("strftime", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		format := params.Args[0].String()
		// Input should be time.Time or string that can be parsed?
		// Ansible `strftime` takes format as argument, and input is epoch or datetime.
		// If input is string, we might need to parse it or assume epoch float.

		val := in.Interface()
		var t time.Time

		switch v := val.(type) {
		case time.Time:
			t = v
		case int:
			t = time.Unix(int64(v), 0)
		case int64:
			t = time.Unix(v, 0)
		case float64:
			t = time.Unix(int64(v), 0)
		case string:
			// Try parsing standard formats? Or assume it's already a string so maybe fail?
			// Ansible often uses this filter on `ansible_date_time` vars which are strings,
			// but usually it's `timestamp | strftime`.
			return in // Fallback
		default:
			return in
		}

		return exec.AsValue(strftime.Format(format, t))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("to_datetime", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// String to datetime
		format := time.RFC3339
		if len(params.Args) > 0 {
			format = params.Args[0].String()
			// Convert python format to go format? That's hard.
			// Assume user passes Go layout if they use this in Go app, OR we need strptime.
			// `strftime` lib only does formatting.
			// For parsing, we might use default Go layouts.
			// If the user expects Python strptime codes, this will fail.
			// Let's assume standard ISO for default, or Go layout for arg.
		}
		t, err := time.Parse(format, in.String())
		if err != nil {
			return exec.AsValue(nil)
		}
		return exec.AsValue(t)
	})
	if err != nil {
		panic(err)
	}

	// date_format filter: formats a time. Usage: {{ my_time | date_format("2006-01-02") }}
	err = gonja.DefaultEnvironment.Filters.Register("date_format", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		t, ok := in.Interface().(time.Time)
		if !ok {
			// Try parsing string if input is string
			if tStr, ok := in.Interface().(string); ok {
				parsed, err := time.Parse(time.RFC3339, tStr)
				if err == nil {
					t = parsed
				} else {
					return exec.AsValue(in.String())
				}
			} else {
				return exec.AsValue(in.String())
			}
		}

		format := time.RFC3339
		if p, ok := params.KwArgs["format"]; ok {
			format = p.String()
		} else if len(params.Args) > 0 {
			format = params.Args[0].String()
		}

		return exec.AsValue(t.Format(format))
	})
	if err != nil {
		panic(err)
	}

	// time_add filter: adds duration. Usage: {{ my_time | time_add("-1h") }}
	err = gonja.DefaultEnvironment.Filters.Register("time_add", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		t, ok := in.Interface().(time.Time)
		if !ok {
			// Try parsing string if input is string
			if tStr, ok := in.Interface().(string); ok {
				parsed, err := time.Parse(time.RFC3339, tStr)
				if err == nil {
					t = parsed
				} else {
					return exec.AsValue(in.String())
				}
			} else {
				return exec.AsValue(in.String())
			}
		}

		durationStr := "0s"
		if p, ok := params.KwArgs["duration"]; ok {
			durationStr = p.String()
		} else if len(params.Args) > 0 {
			durationStr = params.Args[0].String()
		}

		if strings.HasSuffix(durationStr, "d") {
			daysStr := strings.TrimSuffix(durationStr, "d")
			days, err := strconv.Atoi(daysStr)
			if err == nil {
				durationStr = fmt.Sprintf("%dh", days*24)
			}
		}

		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			panic(err)
		}

		return exec.AsValue(t.Add(duration))
	})
	if err != nil {
		panic(err)
	}

	// parse_time filter: parses a string to time. Usage: {{ "2023-01-01" | parse_time("2006-01-02") }}
	err = gonja.DefaultEnvironment.Filters.Register("parse_time", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		timeStr := in.String()
		layout := time.RFC3339
		if p, ok := params.KwArgs["layout"]; ok {
			layout = p.String()
		} else if len(params.Args) > 0 {
			layout = params.Args[0].String()
		}

		t, err := time.Parse(layout, timeStr)
		if err != nil {
			return exec.AsValue(time.Time{}) // Return zero time on error
		}
		return exec.AsValue(t)
	})
	if err != nil {
		panic(err)
	}
}

func registerMiscFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("to_uuid", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Ansible to_uuid generates a UUID v5 based on the input string (namespace OID)
		// We'll use a static namespace for consistency with Ansible if we want deterministic,
		// or just hash it. Ansible uses namespace_url.
		ns := uuid.NameSpaceURL
		u := uuid.NewSHA1(ns, []byte(in.String()))
		return exec.AsValue(u.String())
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("type_debug", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		return exec.AsValue(fmt.Sprintf("%T", in.Interface()))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("bool", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		return exec.AsValue(in.IsTrue())
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("quote", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Shell quote
		s := in.String()
		return exec.AsValue(fmt.Sprintf("'%s'", strings.ReplaceAll(s, "'", "'\\''")))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("random_mac", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		buf := make([]byte, 6)
		_, err := crypto_rand.Read(buf) // Use crypto/rand for MAC generation
		if err != nil {
			panic(err) // Handle error for crypto_rand.Read
		}
		buf[0] = (buf[0] | 2) & 0xfe // unicast, locally administered
		return exec.AsValue(fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", buf[0], buf[1], buf[2], buf[3], buf[4], buf[5]))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("comment", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Wraps text in comments. Style arg?
		// Default #
		lines := strings.Split(in.String(), "\n")
		var out []string
		prefix := "# "
		if len(params.Args) > 0 {
			style := params.Args[0].String()
			switch style {
			case "c":
				prefix = "// "
			case "erlang":
				prefix = "% "
			default:
				prefix = style + " "
			}
		}
		for _, l := range lines {
			out = append(out, prefix+l)
		}
		return exec.AsValue(strings.Join(out, "\n"))
	})
	if err != nil {
		panic(err)
	}
}

func registerDictFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("combine", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// d1 | combine(d2, recursive=False)
		d1 := sanitize(in)
		if len(params.Args) == 0 {
			return in
		}
		d2 := sanitize(params.Args[0])
		recursive := false
		if p, ok := params.KwArgs["recursive"]; ok {
			recursive = p.IsTrue()
		}

		// Helper to merge maps
		var merge func(m1, m2 map[string]any, rec bool) map[string]any
		merge = func(m1, m2 map[string]any, rec bool) map[string]any {
			out := make(map[string]any)
			for k, v := range m1 {
				out[k] = v
			}
			for k, v := range m2 {
				if rec {
					if vMap, ok := v.(map[string]any); ok {
						if existing, ok := out[k]; ok {
							if existingMap, ok := existing.(map[string]any); ok {
								out[k] = merge(existingMap, vMap, true)
								continue
							}
						}
					}
				}
				out[k] = v
			}
			return out
		}

		m1, ok1 := d1.(map[string]any)
		m2, ok2 := d2.(map[string]any)
		if ok1 && ok2 {
			return exec.AsValue(merge(m1, m2, recursive))
		}
		return in
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("dict2items", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		val := sanitize(in)
		m, ok := val.(map[string]any)
		if !ok {
			return exec.AsValue(nil)
		}
		var res []map[string]any
		// Sort keys for deterministic output
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			res = append(res, map[string]any{"key": k, "value": m[k]})
		}
		return exec.AsValue(res)
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("items2dict", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		val := sanitize(in)
		list, ok := val.([]any)
		if !ok {
			return exec.AsValue(nil)
		}

		res := make(map[string]any)
		for _, item := range list {
			m, ok := item.(map[string]any)
			if ok {
				var k, v any
				if key, exists := m["key"]; exists {
					k = key
					v = m["value"]
				} else {
					// Alternative format: [k, v] ? No, items2dict usually takes dict2items output.
					continue
				}
				res[fmt.Sprintf("%v", k)] = v
			}
		}
		return exec.AsValue(res)
	})
	if err != nil {
		panic(err)
	}
}

func registerUrlFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("urlsplit", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		u, err := url.Parse(in.String())
		if err != nil {
			return exec.AsValue(nil)
		}
		// Return dict-like object
		res := map[string]any{
			"scheme":   u.Scheme,
			"netloc":   u.Host,
			"path":     u.Path,
			"query":    u.RawQuery,
			"fragment": u.Fragment,
		}
		return exec.AsValue(res)
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("urldecode", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		res, err := url.QueryUnescape(in.String())
		if err != nil {
			return in
		}
		return exec.AsValue(res)
	})
	if err != nil {
		panic(err)
	}

	// urlencode might be present in Gonja native? If so, we should use Replace or ignore error if we want to override or stick to native.
	// But panic indicates it IS present. Gonja likely has it.
	// We will try Replace or just rely on native if it's good enough.
	// But let's check if we can Replace it to be sure it does what we want (QueryEscape).
	if gonja.DefaultEnvironment.Filters.Exists("urlencode") {
		err = gonja.DefaultEnvironment.Filters.Replace("urlencode", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
			return exec.AsValue(url.QueryEscape(in.String()))
		})
	} else {
		err = gonja.DefaultEnvironment.Filters.Register("urlencode", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
			return exec.AsValue(url.QueryEscape(in.String()))
		})
	}
	if err != nil {
		panic(err)
	}
}

func registerHumanFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("human_readable", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Input bytes (int) -> human string
		val := in.Integer()
		return exec.AsValue(humanize.Bytes(uint64(val)))
	})
	if err != nil {
		panic(err)
	}

	// Alias
	// filesizeformat is also likely native in Gonja. Override it.
	if gonja.DefaultEnvironment.Filters.Exists("filesizeformat") {
		err = gonja.DefaultEnvironment.Filters.Replace("filesizeformat", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
			val := in.Integer()
			return exec.AsValue(humanize.Bytes(uint64(val)))
		})
	} else {
		err = gonja.DefaultEnvironment.Filters.Register("filesizeformat", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
			val := in.Integer()
			return exec.AsValue(humanize.Bytes(uint64(val)))
		})
	}
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("human_to_bytes", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// "1G" -> bytes
		val, err := humanize.ParseBytes(in.String())
		if err != nil {
			return exec.AsValue(0)
		}
		return exec.AsValue(val)
	})
	if err != nil {
		panic(err)
	}
}

func registerMathFilters() {
	var err error
	err = gonja.DefaultEnvironment.Filters.Register("log", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		base := 10.0 // default log10 ? or natural log? Ansible `log` is natural log (base e), `log(base)` custom.
		// Python `math.log(x[, base])`
		val := in.Float()
		if len(params.Args) > 0 {
			base = params.Args[0].Float()
		}

		if base == 10.0 {
			return exec.AsValue(math.Log10(val))
		}
		if base == math.E {
			return exec.AsValue(math.Log(val))
		}
		// log_b(x) = log(x) / log(b)
		return exec.AsValue(math.Log(val) / math.Log(base))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("pow", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		val := in.Float()
		exp := 2.0
		if len(params.Args) > 0 {
			exp = params.Args[0].Float()
		}
		return exec.AsValue(math.Pow(val, exp))
	})
	if err != nil {
		panic(err)
	}

	err = gonja.DefaultEnvironment.Filters.Register("root", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		val := in.Float()
		return exec.AsValue(math.Sqrt(val))
	})
	if err != nil {
		panic(err)
	}
}

func registerFixFilters() {
	var err error
	// Override buggy wordwrap
	// Using Replace instead of Register to override native filter
	err = gonja.DefaultEnvironment.Filters.Replace("wordwrap", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		text := in.String()
		width := 79
		if len(params.Args) > 0 {
			width = params.Args[0].Integer()
		}
		if width <= 0 {
			return in
		}

		// Simple word wrap implementation
		words := strings.Fields(text)
		if len(words) == 0 {
			return in
		}

		var lines []string
		currentLine := words[0]
		currentLen := len(currentLine)

		for _, word := range words[1:] {
			if currentLen+1+len(word) > width {
				lines = append(lines, currentLine)
				currentLine = word
				currentLen = len(word)
			} else {
				currentLine += " " + word
				currentLen += 1 + len(word)
			}
		}
		lines = append(lines, currentLine)
		return exec.AsValue(strings.Join(lines, "\n"))
	})
	if err != nil {
		panic(err)
	}

	// Override center
	err = gonja.DefaultEnvironment.Filters.Replace("center", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		s := in.String()
		width := 80
		if len(params.Args) > 0 {
			width = params.Args[0].Integer()
		}
		if len(s) >= width {
			return in
		}
		pad := width - len(s)
		left := pad / 2
		right := pad - left

		fill := " "
		if len(params.Args) > 1 {
			fill = params.Args[1].String()
		}

		return exec.AsValue(strings.Repeat(fill, left) + s + strings.Repeat(fill, right))
	})
	if err != nil {
		panic(err)
	}
}
