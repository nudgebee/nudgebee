package api

import (
	"fmt"
	"strconv"
	"strings"
)

// nbctlOperation is one root field of a GraphQL document sent by nbctl, with
// its arguments resolved against the request variables.
type nbctlOperation struct {
	Field string
	Args  map[string]any
}

// parseNbctlOperation reads the GraphQL that nbctl sends for its workspace
// commands: one query operation with one root field whose arguments are
// literals or variables. That is all nbctl's metrics and logs commands use
// (see nbctl cmd/workspace_contract_test.go); anything else (mutations,
// subscriptions, fragments, directives, several root fields) is refused, so
// the proxy never has to guess what a document means.
func parseNbctlOperation(document string, variables map[string]any) (nbctlOperation, error) {
	p := &gqlParser{src: document}
	if err := p.document(); err != nil {
		return nbctlOperation{}, err
	}
	if p.field == "" {
		return nbctlOperation{}, fmt.Errorf("no field in query")
	}
	args, err := resolveGQLValue(p.args, variables)
	if err != nil {
		return nbctlOperation{}, err
	}
	resolved, _ := args.(map[string]any)
	return nbctlOperation{Field: p.field, Args: resolved}, nil
}

// gqlVariable marks a `$name` reference until it is resolved.
type gqlVariable string

type gqlParser struct {
	src   string
	pos   int
	field string
	args  map[string]any
}

func (p *gqlParser) document() error {
	p.skip()
	if p.peekName() {
		switch kind := p.name(); kind {
		case "query":
		case "mutation", "subscription", "fragment":
			return fmt.Errorf("%s is not available in Nubi's workspace", kind)
		default:
			return fmt.Errorf("unsupported operation %q", kind)
		}
		p.skip()
		if p.peekName() {
			p.name()
		}
		p.skip()
		if p.peek() == '(' {
			if err := p.skipBalanced('(', ')'); err != nil {
				return err
			}
		}
	}
	p.skip()
	if p.peek() == '@' {
		return fmt.Errorf("directives are not supported")
	}
	if err := p.expect('{'); err != nil {
		return err
	}
	p.skip()
	if !p.peekName() {
		return fmt.Errorf("expected a field")
	}
	p.field = p.name()
	p.skip()
	if p.peek() == ':' {
		return fmt.Errorf("field aliases are not supported")
	}
	p.args = map[string]any{}
	if p.peek() == '(' {
		p.pos++
		for {
			p.skip()
			if p.peek() == ')' {
				p.pos++
				break
			}
			if !p.peekName() {
				return fmt.Errorf("expected an argument name at offset %d", p.pos)
			}
			name := p.name()
			p.skip()
			if err := p.expect(':'); err != nil {
				return err
			}
			v, err := p.value()
			if err != nil {
				return err
			}
			p.args[name] = v
		}
	}
	p.skip()
	if p.peek() == '@' {
		return fmt.Errorf("directives are not supported")
	}
	if p.peek() == '{' {
		if err := p.skipBalanced('{', '}'); err != nil {
			return err
		}
	}
	p.skip()
	if p.peekName() {
		return fmt.Errorf("only one field per request is supported")
	}
	if err := p.expect('}'); err != nil {
		return err
	}
	p.skip()
	if p.pos != len(p.src) {
		return fmt.Errorf("only one operation per request is supported")
	}
	return nil
}

func (p *gqlParser) value() (any, error) {
	p.skip()
	switch c := p.peek(); {
	case c == '$':
		p.pos++
		if !p.peekName() {
			return nil, fmt.Errorf("expected a variable name")
		}
		return gqlVariable(p.name()), nil
	case c == '"':
		return p.str()
	case c == '{':
		p.pos++
		obj := map[string]any{}
		for {
			p.skip()
			if p.peek() == '}' {
				p.pos++
				return obj, nil
			}
			if !p.peekName() {
				return nil, fmt.Errorf("expected an object field at offset %d", p.pos)
			}
			key := p.name()
			p.skip()
			if err := p.expect(':'); err != nil {
				return nil, err
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			obj[key] = v
		}
	case c == '[':
		p.pos++
		list := []any{}
		for {
			p.skip()
			if p.peek() == ']' {
				p.pos++
				return list, nil
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
	case c == '-' || (c >= '0' && c <= '9'):
		start := p.pos
		p.pos++
		for p.pos < len(p.src) && strings.ContainsRune("0123456789.eE+-", rune(p.src[p.pos])) {
			p.pos++
		}
		n, err := strconv.ParseFloat(p.src[start:p.pos], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", p.src[start:p.pos])
		}
		return n, nil
	case p.peekName():
		switch word := p.name(); word {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		default:
			return word, nil // enum value
		}
	default:
		return nil, fmt.Errorf("unexpected %q at offset %d", string(c), p.pos)
	}
}

func (p *gqlParser) str() (string, error) {
	if strings.HasPrefix(p.src[p.pos:], `"""`) {
		return "", fmt.Errorf("block strings are not supported")
	}
	start := p.pos
	p.pos++
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '\\':
			p.pos += 2
		case '"':
			p.pos++
			s, err := strconv.Unquote(p.src[start:p.pos])
			if err != nil {
				return "", fmt.Errorf("invalid string at offset %d", start)
			}
			return s, nil
		default:
			p.pos++
		}
	}
	return "", fmt.Errorf("unterminated string")
}

// skipBalanced skips a bracketed group, honouring strings and comments inside
// it.
func (p *gqlParser) skipBalanced(open, closeCh byte) error {
	depth := 0
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; c {
		case '"':
			if _, err := p.str(); err != nil {
				return err
			}
			continue
		case '#':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
			continue
		case open:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				p.pos++
				return nil
			}
		}
		p.pos++
	}
	return fmt.Errorf("unbalanced %q", string(open))
}

func (p *gqlParser) skip() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '#':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		// GraphQL whitespace is ASCII only; checking bytes keeps multi-byte
		// UTF-8 characters intact.
		case ',', ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *gqlParser) peek() byte {
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *gqlParser) peekName() bool {
	c := p.peek()
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func (p *gqlParser) name() string {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			p.pos++
			continue
		}
		break
	}
	return p.src[start:p.pos]
}

func (p *gqlParser) expect(c byte) error {
	p.skip()
	if p.peek() != c {
		return fmt.Errorf("expected %q at offset %d", string(c), p.pos)
	}
	p.pos++
	return nil
}

// resolveGQLValue replaces variable references with their values. A missing
// variable is an error rather than null, so a typo cannot silently widen a
// query.
func resolveGQLValue(v any, variables map[string]any) (any, error) {
	switch t := v.(type) {
	case gqlVariable:
		val, ok := variables[string(t)]
		if !ok {
			return nil, fmt.Errorf("variable $%s is not provided", string(t))
		}
		return val, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			r, err := resolveGQLValue(item, variables)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			r, err := resolveGQLValue(item, variables)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}
