package tool

import (
	"regexp"
	"strings"
)

var (
	reBearer = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._\-]+`)
	reSK     = regexp.MustCompile(`\bsk-[A-Za-z0-9]{8,}`)
	reHexKey = regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`)
)

// RedactSecrets masks common secret shapes in tool output before it enters history.
func RedactSecrets(_ string, content string) string {
	if content == "" {
		return content
	}
	s := reBearer.ReplaceAllString(content, "${1}***")
	s = reSK.ReplaceAllString(s, "sk-***")
	s = reHexKey.ReplaceAllStringFunc(s, func(in string) string {
		if len(in) < 32 {
			return in
		}
		return in[:4] + strings.Repeat("*", 8)
	})
	return s
}
