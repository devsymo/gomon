// Package protocol implements the Xymon (and legacy Big Brother) wire
// protocol: the message formats sent by xymon clients, ext scripts and
// tools to the xymond daemon on port 1984 (plaintext) or 1985 (TLS).
package protocol

import "strings"

// Color is a Xymon status color. The numeric order matches xymond's own
// ordering so that "worst color" comparisons behave identically.
type Color int

const (
	Green Color = iota
	Clear
	Blue
	Purple
	Yellow
	Red
)

// ColorClient is the pseudo color "client" that lets a plain status message
// carry client data (used by some Big Brother era tools).
const ColorClient Color = -2

var colorNames = map[Color]string{
	Green:  "green",
	Clear:  "clear",
	Blue:   "blue",
	Purple: "purple",
	Yellow: "yellow",
	Red:    "red",
}

func (c Color) String() string {
	if n, ok := colorNames[c]; ok {
		return n
	}
	if c == ColorClient {
		return "client"
	}
	return "unknown"
}

// ParseColor parses a color name. Case-insensitive.
func ParseColor(s string) (Color, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "green":
		return Green, true
	case "clear":
		return Clear, true
	case "blue":
		return Blue, true
	case "purple":
		return Purple, true
	case "yellow":
		return Yellow, true
	case "red":
		return Red, true
	case "client":
		return ColorClient, true
	}
	return 0, false
}

// IsAlert reports whether the color is an alerting state (yellow or red).
func (c Color) IsAlert() bool { return c == Yellow || c == Red }

// Worse returns the more severe of two colors.
func Worse(a, b Color) Color {
	if b > a {
		return b
	}
	return a
}
