package search

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/tseiman/SecretTUIVault/internal/vault"
)

type field uint8

const (
	fieldAny field = iota
	fieldName
	fieldTag
)

type term struct {
	field field
	value string
}

type lexicalToken struct {
	text             string
	quoted           bool
	quotedWithPrefix bool
}

// Query is an OR of groups. Terms within each group are joined by AND.
type Query struct{ groups [][]term }

type Result struct {
	Entry vault.Entry
	Score int
}

func Parse(input string) (Query, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return Query{}, err
	}
	if len(tokens) == 0 {
		return Query{}, nil
	}
	var groups [][]term
	expectTerm := true
	and := false
	for _, token := range tokens {
		if !token.quoted && strings.EqualFold(token.text, "AND") {
			if expectTerm {
				return Query{}, fmt.Errorf("unexpected AND operator")
			}
			expectTerm, and = true, true
			continue
		}
		t, err := parseTerm(token.text, token.quoted && !token.quotedWithPrefix)
		if err != nil {
			return Query{}, err
		}
		if len(groups) == 0 || !and {
			groups = append(groups, []term{t})
		} else {
			groups[len(groups)-1] = append(groups[len(groups)-1], t)
		}
		expectTerm, and = false, false
	}
	if expectTerm {
		return Query{}, fmt.Errorf("query ends with AND operator")
	}
	return Query{groups: groups}, nil
}

func (q Query) Filter(entries []vault.Entry) []Result {
	results := make([]Result, 0, len(entries))
	for _, entry := range entries {
		score, ok := q.match(entry)
		if ok {
			results = append(results, Result{Entry: entry, Score: score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		in, jn := strings.ToLower(results[i].Entry.Name), strings.ToLower(results[j].Entry.Name)
		if in != jn {
			return in < jn
		}
		return results[i].Entry.ID < results[j].Entry.ID
	})
	return results
}

func (q Query) match(entry vault.Entry) (int, bool) {
	if len(q.groups) == 0 {
		return 0, true
	}
	best := -1
	for _, group := range q.groups {
		total, matches := 0, true
		for _, t := range group {
			score, ok := t.match(entry)
			if !ok {
				matches = false
				break
			}
			total += score
		}
		if matches && total > best {
			best = total
		}
	}
	return best, best >= 0
}

func (t term) match(entry vault.Entry) (int, bool) {
	values := []string{}
	switch t.field {
	case fieldName:
		values = append(values, entry.Name)
	case fieldTag:
		values = append(values, entry.Tags...)
	default:
		values = append(values, entry.Name, entry.Description)
		values = append(values, entry.Tags...)
	}
	best := -1
	for _, value := range values {
		if score, ok := fuzzyScore(t.value, value); ok && score > best {
			best = score
		}
	}
	return best, best >= 0
}

func fuzzyScore(pattern, candidate string) (int, bool) {
	pattern = strings.ToLower(pattern)
	candidate = strings.ToLower(candidate)
	if idx := strings.Index(candidate, pattern); idx >= 0 {
		return 1000 - idx*2 - (len([]rune(candidate)) - len([]rune(pattern))), true
	}
	p, c := []rune(pattern), []rune(candidate)
	pi, first, last := 0, -1, -1
	for i, r := range c {
		if pi < len(p) && r == p[pi] {
			if first < 0 {
				first = i
			}
			last = i
			pi++
		}
	}
	if pi != len(p) {
		return 0, false
	}
	return 500 - (last-first+1-len(p))*4 - first, true
}

func tokenize(input string) ([]lexicalToken, error) {
	r := []rune(input)
	var tokens []lexicalToken
	for i := 0; i < len(r); {
		for i < len(r) && unicode.IsSpace(r[i]) {
			i++
		}
		if i == len(r) {
			break
		}
		start := i
		for i < len(r) && !unicode.IsSpace(r[i]) && r[i] != '"' {
			i++
		}
		prefix := string(r[start:i])
		if i < len(r) && r[i] == '"' {
			i++
			var value strings.Builder
			closed := false
			for i < len(r) {
				if r[i] == '\\' && i+1 < len(r) && (r[i+1] == '"' || r[i+1] == '\\') {
					value.WriteRune(r[i+1])
					i += 2
					continue
				}
				if r[i] == '"' {
					i++
					closed = true
					break
				}
				value.WriteRune(r[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted term")
			}
			if i < len(r) && !unicode.IsSpace(r[i]) {
				return nil, fmt.Errorf("unexpected character after quoted term")
			}
			tokens = append(tokens, lexicalToken{text: prefix + value.String(), quoted: true, quotedWithPrefix: prefix != ""})
		} else {
			if prefix != "" {
				tokens = append(tokens, lexicalToken{text: prefix})
			}
		}
	}
	return tokens, nil
}

func parseTerm(token string, literal bool) (term, error) {
	f := fieldAny
	value := token
	if idx := strings.IndexRune(token, ':'); idx >= 0 && !literal {
		prefix := strings.ToUpper(token[:idx])
		switch prefix {
		case "TAG":
			f = fieldTag
			value = token[idx+1:]
		case "NAME":
			f = fieldName
			value = token[idx+1:]
		}
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return term{}, fmt.Errorf("empty search term")
	}
	return term{field: f, value: value}, nil
}
