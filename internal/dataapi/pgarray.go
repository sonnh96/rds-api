package dataapi

import "fmt"

// pgArrayNode is one entry of a decoded PostgreSQL array literal: either a
// nested array, a NULL (value == nil) or a scalar in its text form.
type pgArrayNode struct {
	isArray  bool
	children []pgArrayNode
	value    *string
}

// parsePGArray decodes the text representation PostgreSQL uses for arrays,
// e.g. {1,2,3}, {"a,b",NULL} or {{1,2},{3,4}}.
func parsePGArray(literal string) ([]pgArrayNode, error) {
	p := &pgArrayParser{input: literal}
	p.skipSpace()
	nodes, err := p.parseArray()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.input) {
		return nil, fmt.Errorf("trailing characters in array literal %q", literal)
	}
	return nodes, nil
}

type pgArrayParser struct {
	input string
	pos   int
}

func (p *pgArrayParser) skipSpace() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t' || p.input[p.pos] == '\n' || p.input[p.pos] == '\r') {
		p.pos++
	}
}

func (p *pgArrayParser) parseArray() ([]pgArrayNode, error) {
	if p.pos >= len(p.input) || p.input[p.pos] != '{' {
		return nil, fmt.Errorf("array literal %q does not start with '{'", p.input)
	}
	p.pos++ // consume '{'

	nodes := []pgArrayNode{}
	p.skipSpace()
	if p.pos < len(p.input) && p.input[p.pos] == '}' {
		p.pos++
		return nodes, nil
	}

	for {
		p.skipSpace()
		node, err := p.parseElement()
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)

		p.skipSpace()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("unterminated array literal %q", p.input)
		}
		switch p.input[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return nodes, nil
		default:
			return nil, fmt.Errorf("unexpected character %q in array literal %q", p.input[p.pos], p.input)
		}
	}
}

func (p *pgArrayParser) parseElement() (pgArrayNode, error) {
	if p.pos >= len(p.input) {
		return pgArrayNode{}, fmt.Errorf("unterminated array literal %q", p.input)
	}

	if p.input[p.pos] == '{' {
		children, err := p.parseArray()
		if err != nil {
			return pgArrayNode{}, err
		}
		return pgArrayNode{isArray: true, children: children}, nil
	}

	if p.input[p.pos] == '"' {
		text, err := p.parseQuoted()
		if err != nil {
			return pgArrayNode{}, err
		}
		return pgArrayNode{value: &text}, nil
	}

	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] != ',' && p.input[p.pos] != '}' {
		p.pos++
	}
	text := trimSpace(p.input[start:p.pos])
	// An unquoted NULL is the null element; a quoted "NULL" is the string.
	if equalFold(text, "NULL") {
		return pgArrayNode{}, nil
	}
	return pgArrayNode{value: &text}, nil
}

func (p *pgArrayParser) parseQuoted() (string, error) {
	p.pos++ // consume the opening quote
	var out []byte
	for p.pos < len(p.input) {
		c := p.input[p.pos]
		switch c {
		case '\\':
			p.pos++
			if p.pos >= len(p.input) {
				return "", fmt.Errorf("unterminated escape in array literal %q", p.input)
			}
			out = append(out, p.input[p.pos])
			p.pos++
		case '"':
			p.pos++
			return string(out), nil
		default:
			out = append(out, c)
			p.pos++
		}
	}
	return "", fmt.Errorf("unterminated quoted element in array literal %q", p.input)
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
