package templates

import "strconv"

// formatInt formats an integer using "." as the thousands separator (German style).
// E.g. 108577 -> "108.577", -1234 -> "-1.234".
func formatInt(n int) string {
	s := strconv.Itoa(n)
	sign := ""
	if len(s) > 0 && s[0] == '-' {
		sign = "-"
		s = s[1:]
	}
	// Insert "." every 3 digits from the right.
	if len(s) <= 3 {
		return sign + s
	}
	first := len(s) % 3
	out := make([]byte, 0, len(s)+len(s)/3)
	if first > 0 {
		out = append(out, s[:first]...)
	}
	for i := first; i < len(s); i += 3 {
		if len(out) > 0 {
			out = append(out, '.')
		}
		out = append(out, s[i:i+3]...)
	}
	return sign + string(out)
}
