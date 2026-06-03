package workflow

import (
	"fmt"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/nikolalohinski/gonja/v2"
	"github.com/nikolalohinski/gonja/v2/exec"
)

func init() {
	registerTests()
}

func registerTests() {
	// Helper to register tests
	register := func(name string, test func(*exec.Evaluator, *exec.Value, *exec.VarArgs) (bool, error)) {
		if gonja.DefaultEnvironment.Tests.Exists(name) {
			err := gonja.DefaultEnvironment.Tests.Replace(name, test)
			if err != nil {
				panic(fmt.Sprintf("failed to replace test %s: %v", name, err))
			}
		} else {
			err := gonja.DefaultEnvironment.Tests.Register(name, test)
			if err != nil {
				panic(fmt.Sprintf("failed to register test %s: %v", name, err))
			}
		}
	}

	// Path/File Tests
	// register("abs", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	// 	if !in.IsString() {
	// 		return false, nil
	// 	}
	// 	return filepath.IsAbs(in.String()), nil
	// })

	// register("directory", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	// 	if !in.IsString() {
	// 		return false, nil
	// 	}
	// 	info, err := os.Stat(in.String())
	// 	return err == nil && info.IsDir(), nil
	// })

	// register("exists", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	// 	if !in.IsString() {
	// 		return false, nil
	// 	}
	// 	_, err := os.Stat(in.String())
	// 	return err == nil, nil
	// })

	// register("file", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	// 	if !in.IsString() {
	// 		return false, nil
	// 	}
	// 	info, err := os.Stat(in.String())
	// 	return err == nil && !info.IsDir(), nil
	// })

	// register("link", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	// 	if !in.IsString() {
	// 		return false, nil
	// 	}
	// 	info, err := os.Lstat(in.String())
	// 	return err == nil && (info.Mode()&os.ModeSymlink != 0), nil
	// })

	// register("link_exists", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	// 	if !in.IsString() {
	// 		return false, nil
	// 	}
	// 	_, err := os.Lstat(in.String())
	// 	return err == nil, nil
	// })

	// register("same_file", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	// 	if !in.IsString() || len(params.Args) == 0 {
	// 		return false, nil
	// 	}
	// 	other := params.Args[0].String()
	// 	info1, err1 := os.Stat(in.String())
	// 	info2, err2 := os.Stat(other)
	// 	if err1 != nil || err2 != nil {
	// 		return false, nil
	// 	}
	// 	return os.SameFile(info1, info2), nil
	// })

	// register("mount", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
	// 	// Not implemented
	// 	return false, nil
	// })

	// Logic/Type Tests
	register("boolean", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsBool(), nil
	})

	register("true", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsBool() && in.IsTrue(), nil
	})

	register("false", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsBool() && !in.IsTrue(), nil
	})

	register("falsy", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return !in.IsTrue(), nil
	})

	register("truthy", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsTrue(), nil
	})

	register("defined", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return !in.IsNil(), nil
	})

	register("undefined", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsNil(), nil
	})

	register("none", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsNil(), nil
	})

	register("float", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsFloat(), nil
	})

	register("integer", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsInteger(), nil
	})

	register("string", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsString(), nil
	})

	register("number", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsInteger() || in.IsFloat(), nil
	})

	register("sequence", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsList(), nil
	})

	register("mapping", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsDict(), nil
	})

	register("iterable", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.IsList() || in.IsDict() || in.IsString(), nil
	})

	register("callable", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		val := in.Interface()
		if val == nil {
			return false, nil
		}
		v := reflect.ValueOf(val)
		return v.Kind() == reflect.Func, nil
	})

	// Math
	register("even", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsInteger() {
			return false, nil
		}
		return in.Integer()%2 == 0, nil
	})

	register("odd", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsInteger() {
			return false, nil
		}
		return in.Integer()%2 != 0, nil
	})

	register("divisibleby", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsInteger() || len(params.Args) == 0 {
			return false, nil
		}
		div := params.Args[0].Integer()
		if div == 0 {
			return false, nil
		}
		return in.Integer()%div == 0, nil
	})

	register("nan", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if in.IsFloat() {
			return math.IsNaN(in.Float()), nil
		}
		return false, nil
	})

	// List/String
	register("lower", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() {
			return false, nil
		}
		s := in.String()
		return s == strings.ToLower(s), nil
	})

	register("upper", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() {
			return false, nil
		}
		s := in.String()
		return s == strings.ToUpper(s), nil
	})

	register("in", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if len(params.Args) == 0 {
			return false, nil
		}
		seq := params.Args[0]
		if seq.IsList() {
			found := false
			seq.Iterate(func(idx, count int, item, _ *exec.Value) bool {
				if item.String() == in.String() {
					found = true
					return false
				}
				return true
			}, func() {})
			return found, nil
		}
		if seq.IsString() {
			return strings.Contains(seq.String(), in.String()), nil
		}
		if seq.IsDict() {
			found := false
			seq.Iterate(func(idx, count int, key, val *exec.Value) bool {
				if key.String() == in.String() {
					found = true
					return false
				}
				return true
			}, func() {})
			return found, nil
		}
		return false, nil
	})

	register("contains", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if len(params.Args) == 0 {
			return false, nil
		}
		val := params.Args[0]
		if in.IsList() {
			found := false
			in.Iterate(func(idx, count int, item, _ *exec.Value) bool {
				if item.String() == val.String() {
					found = true
					return false
				}
				return true
			}, func() {})
			return found, nil
		}
		if in.IsString() {
			return strings.Contains(in.String(), val.String()), nil
		}
		if in.IsDict() {
			found := false
			in.Iterate(func(idx, count int, key, v *exec.Value) bool {
				if key.String() == val.String() {
					found = true
					return false
				}
				return true
			}, func() {})
			return found, nil
		}
		return false, nil
	})

	register("match", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() || len(params.Args) == 0 {
			return false, nil
		}
		pattern := params.Args[0].String()
		// Ansible match implies full match (or from start)
		if !strings.HasPrefix(pattern, "^") {
			pattern = "^" + pattern
		}
		matched, _ := regexp.MatchString(pattern, in.String())
		return matched, nil
	})

	register("search", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() || len(params.Args) == 0 {
			return false, nil
		}
		pattern := params.Args[0].String()
		matched, _ := regexp.MatchString(pattern, in.String())
		return matched, nil
	})

	register("regex", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() || len(params.Args) == 0 {
			return false, nil
		}
		pattern := params.Args[0].String()
		if !strings.HasPrefix(pattern, "^") {
			pattern = "^" + pattern
		}
		matched, _ := regexp.MatchString(pattern, in.String())
		return matched, nil
	})

	register("url", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() {
			return false, nil
		}
		u, err := url.ParseRequestURI(in.String())
		return err == nil && u.Scheme != "" && u.Host != "", nil
	})

	register("uri", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() {
			return false, nil
		}
		_, err := url.ParseRequestURI(in.String())
		return err == nil, nil
	})

	register("urn", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() {
			return false, nil
		}
		return strings.HasPrefix(in.String(), "urn:"), nil
	})

	checkResult := func(key string, expected bool) func(*exec.Evaluator, *exec.Value, *exec.VarArgs) (bool, error) {
		return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
			if !in.IsDict() {
				return false, nil
			}
			var result bool
			in.Iterate(func(idx, count int, k, v *exec.Value) bool {
				if k.String() == key {
					result = v.IsTrue()
					return false
				}
				return true
			}, func() {})
			return result == expected, nil
		}
	}

	register("failed", checkResult("failed", true))
	register("changed", checkResult("changed", true))
	register("success", checkResult("failed", false))
	register("skipped", checkResult("skipped", true))

	// Missing tests implementation
	register("escaped", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		return in.Safe, nil
	})

	register("filter", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() {
			return false, nil
		}
		return gonja.DefaultEnvironment.Filters.Exists(in.String()), nil
	})

	register("test", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() {
			return false, nil
		}
		return gonja.DefaultEnvironment.Tests.Exists(in.String()), nil
	})

	register("finished", checkResult("finished", true))
	register("started", checkResult("started", true))
	register("timedout", checkResult("timed_out", true)) // assuming key is timed_out or timedout, usually checked both? Ansible checks task 'finished' state

	register("reachable", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsDict() {
			return false, nil
		}
		// check if "unreachable" is false or missing
		isUnreachable := false
		in.Iterate(func(idx, count int, k, v *exec.Value) bool {
			if k.String() == "unreachable" && v.IsTrue() {
				isUnreachable = true
				return false
			}
			return true
		}, func() {})
		return !isUnreachable, nil
	})

	register("unreachable", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsDict() {
			return false, nil
		}
		isUnreachable := false
		in.Iterate(func(idx, count int, k, v *exec.Value) bool {
			if k.String() == "unreachable" && v.IsTrue() {
				isUnreachable = true
				return false
			}
			return true
		}, func() {})
		return isUnreachable, nil
	})

	// Vault tests - stubs or basic checks
	register("vault_encrypted", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsString() {
			return false, nil
		}
		return strings.HasPrefix(in.String(), "$ANSIBLE_VAULT;"), nil
	})

	register("version", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if len(params.Args) == 0 {
			return false, nil
		}

		args := params.Args
		// Gonja bug/feature: sometimes args are wrapped in a list if passed with parens?
		if len(args) == 1 && args[0].IsList() {
			unpacked := make([]*exec.Value, 0)
			args[0].Iterate(func(_, _ int, item, _ *exec.Value) bool {
				unpacked = append(unpacked, item)
				return true
			}, func() {})
			args = unpacked
		}

		if len(args) == 0 {
			return false, nil
		}

		verStr := args[0].String()
		op := "eq"
		if len(args) > 1 {
			op = args[1].String()
		}
		valStr := in.String()

		v1, err1 := version.NewVersion(valStr)
		v2, err2 := version.NewVersion(verStr)

		// Fallback to string comparison if parsing fails (Ansible behavior for loose versions?)
		// Actually Ansible uses LooseVersion which handles 1.0 vs 2.0.
		// hashicorp/go-version handles 1.0 vs 2.0 well.
		// If error, it might be non-version strings.
		if err1 != nil || err2 != nil {
			// Fallback to string comparison
			switch op {
			case "eq", "==", "equalto":
				return valStr == verStr, nil
			case "ne", "!=":
				return valStr != verStr, nil
			case "lt", "<":
				return valStr < verStr, nil
			case "le", "<=":
				return valStr <= verStr, nil
			case "gt", ">":
				return valStr > verStr, nil
			case "ge", ">=":
				return valStr >= verStr, nil
			}
			return false, nil
		}

		switch op {
		case "eq", "==", "equalto":
			return v1.Equal(v2), nil
		case "ne", "!=":
			return !v1.Equal(v2), nil
		case "lt", "<":
			return v1.LessThan(v2), nil
		case "le", "<=":
			return v1.LessThanOrEqual(v2), nil
		case "gt", ">":
			return v1.GreaterThan(v2), nil
		case "ge", ">=":
			return v1.GreaterThanOrEqual(v2), nil
		}
		return false, nil
	})

	register("subset", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsList() || len(params.Args) == 0 || !params.Args[0].IsList() {
			return false, nil
		}

		target := make(map[string]bool)
		params.Args[0].Iterate(func(_, _ int, item, _ *exec.Value) bool {
			target[item.String()] = true
			return true
		}, func() {})

		isSubset := true
		in.Iterate(func(_, _ int, item, _ *exec.Value) bool {
			if !target[item.String()] {
				isSubset = false
				return false
			}
			return true
		}, func() {})

		return isSubset, nil
	})

	register("superset", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		if !in.IsList() || len(params.Args) == 0 || !params.Args[0].IsList() {
			return false, nil
		}

		source := make(map[string]bool)
		in.Iterate(func(_, _ int, item, _ *exec.Value) bool {
			source[item.String()] = true
			return true
		}, func() {})

		isSuperset := true
		params.Args[0].Iterate(func(_, _ int, item, _ *exec.Value) bool {
			if !source[item.String()] {
				isSuperset = false
				return false
			}
			return true
		}, func() {})

		return isSuperset, nil
	})

	// Collection tests
	register("all", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		// Checks if all items in list are truthy
		if !in.IsList() {
			return false, nil
		}
		result := true
		in.Iterate(func(_, _ int, item, _ *exec.Value) bool {
			if !item.IsTrue() {
				result = false
				return false // stop
			}
			return true
		}, func() {})
		return result, nil
	})

	register("any", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
		// Checks if any item in list is truthy
		if !in.IsList() {
			return false, nil
		}
		result := false
		in.Iterate(func(_, _ int, item, _ *exec.Value) bool {
			if item.IsTrue() {
				result = true
				return false // stop
			}
			return true
		}, func() {})
		return result, nil
	})

	// Comparison tests
	compare := func(op string) func(*exec.Evaluator, *exec.Value, *exec.VarArgs) (bool, error) {
		return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) (bool, error) {
			if len(params.Args) == 0 {
				return false, nil
			}
			other := params.Args[0]

			switch op {
			case "eq":
				return in.EqualValueTo(other), nil
			case "ne":
				return !in.EqualValueTo(other), nil
			}

			if in.IsInteger() && other.IsInteger() {
				v1 := in.Integer()
				v2 := other.Integer()
				switch op {
				case "lt":
					return v1 < v2, nil
				case "le":
					return v1 <= v2, nil
				case "gt":
					return v1 > v2, nil
				case "ge":
					return v1 >= v2, nil
				}
			}
			if in.IsFloat() || other.IsFloat() {
				v1 := in.Float()
				v2 := other.Float()
				switch op {
				case "lt":
					return v1 < v2, nil
				case "le":
					return v1 <= v2, nil
				case "gt":
					return v1 > v2, nil
				case "ge":
					return v1 >= v2, nil
				}
			}
			if in.IsString() && other.IsString() {
				v1 := in.String()
				v2 := other.String()
				switch op {
				case "lt":
					return v1 < v2, nil
				case "le":
					return v1 <= v2, nil
				case "gt":
					return v1 > v2, nil
				case "ge":
					return v1 >= v2, nil
				}
			}

			return false, nil
		}
	}

	register("eq", compare("eq"))
	register("equalto", compare("eq"))
	register("ne", compare("ne"))
	register("lt", compare("lt"))
	register("le", compare("le"))
	register("gt", compare("gt"))
	register("ge", compare("ge"))
	register("greaterthan", compare("gt"))
	register("lessthan", compare("lt"))
}
