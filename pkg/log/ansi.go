package log

import (
	"regexp"
	"strconv"
	"strings"
)

var colorCodes = map[string]int{
	"black":   30,
	"red":     31,
	"green":   32,
	"yellow":  33,
	"blue":    34,
	"magenta": 35,
	"cyan":    36,
	"white":   37,
}

// colorize wraps s in ANSI escape codes for style, which is a color name optionally followed by
// "+" and attributes: "b" for bold and "h" for high intensity, e.g. "red+b".
// Unknown colors leave s unchanged.
func colorize(s, style string) string {
	name, attributes, _ := strings.Cut(style, "+")
	code, ok := colorCodes[name]
	if !ok {
		return s
	}

	params := "0;"
	if strings.Contains(attributes, "b") {
		params += "1;"
	}
	if strings.Contains(attributes, "h") {
		code += 60
	}
	return "\x1b[" + params + strconv.Itoa(code) + "m" + s + "\x1b[0m"
}

// ansiEscape matches ANSI escape sequences. It is the pattern of the ansi-regex npm package.
var ansiEscape = regexp.MustCompile("[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))")

// stripANSI removes ANSI escape sequences, like colors, from s.
func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}
