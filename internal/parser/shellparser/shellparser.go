// Package shellparser parses control-shell command lines into a command name
// and its arguments, honoring single quotes, double quotes, and backslash
// escapes so arguments may contain spaces.
package shellparser

import "fmt"

// Command is a parsed control-shell line.
type Command struct {
	Name string
	Args []string
}

// Parse tokenizes line into a Command. An empty or whitespace-only line yields
// a Command with an empty Name.
func Parse(line string) (Command, error) {
	tokens, err := tokenize(line)
	if err != nil {
		return Command{}, err
	}
	if len(tokens) == 0 {
		return Command{}, nil
	}
	return Command{Name: tokens[0], Args: tokens[1:]}, nil
}

// tokenize splits s into shell-like tokens.
func tokenize(s string) ([]string, error) {
	var tokens []string
	var cur []rune
	inTok := false

	const (
		none = iota
		single
		double
	)
	quote := none

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch quote {
		case single:
			if c == '\'' {
				quote = none
			} else {
				cur = append(cur, c)
			}
		case double:
			if c == '"' {
				quote = none
			} else if c == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
				cur = append(cur, runes[i+1])
				i++
			} else {
				cur = append(cur, c)
			}
		default:
			switch {
			case c == '\'':
				quote = single
				inTok = true
			case c == '"':
				quote = double
				inTok = true
			case c == '\\' && i+1 < len(runes):
				cur = append(cur, runes[i+1])
				i++
				inTok = true
			case c == ' ' || c == '\t' || c == '\n':
				if inTok {
					tokens = append(tokens, string(cur))
					cur = cur[:0]
					inTok = false
				}
			default:
				cur = append(cur, c)
				inTok = true
			}
		}
	}
	if quote != none {
		return nil, fmt.Errorf("unterminated quote")
	}
	if inTok {
		tokens = append(tokens, string(cur))
	}
	return tokens, nil
}
