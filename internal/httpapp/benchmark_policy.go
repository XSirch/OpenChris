package httpapp

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"

	"openchris/conselho/web"
)

// The fixed comparator page is shared verbatim with Django. Its inline blocks
// are authorized by exact hashes, without permitting arbitrary inline code.
func benchmarkPolicy() string {
	data, _ := web.Files.ReadFile("benchmark.html")
	html := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	directive := func(tag string) string {
		value := "'self'"
		for _, match := range regexp.MustCompile(`(?s)<`+tag+`>(.*?)</`+tag+`>`).FindAllStringSubmatch(html, -1) {
			sum := sha256.Sum256([]byte(match[1]))
			value += " 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		}
		return value
	}
	return "default-src 'self'; script-src " + directive("script") + "; style-src " + directive("style") + "; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'none'"
}
