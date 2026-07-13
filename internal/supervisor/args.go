package supervisor

import "fmt"

// splitArgs splits a command string into argv the way a shell tokenizer would,
// honoring single quotes, double quotes, and backslash escapes. taskmaster runs
// programs directly (no shell), so this is how "cmd" becomes exec arguments.
func splitArgs(s string) ([]string, error) {
	var args []string
	var cur []rune
	inArg := false

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
			} else if c == '\\' && i+1 < len(runes) {
				next := runes[i+1]
				// Inside double quotes only \" and \\ are escapes.
				if next == '"' || next == '\\' {
					cur = append(cur, next)
					i++
				} else {
					cur = append(cur, c)
				}
			} else {
				cur = append(cur, c)
			}
		default: // unquoted
			switch {
			case c == '\'':
				quote = single
				inArg = true
			case c == '"':
				quote = double
				inArg = true
			case c == '\\' && i+1 < len(runes):
				cur = append(cur, runes[i+1])
				i++
				inArg = true
			case c == ' ' || c == '\t' || c == '\n':
				if inArg {
					args = append(args, string(cur))
					cur = cur[:0]
					inArg = false
				}
			default:
				cur = append(cur, c)
				inArg = true
			}
		}
	}
	if quote != none {
		return nil, fmt.Errorf("unterminated quote")
	}
	if inArg {
		args = append(args, string(cur))
	}
	return args, nil
}
