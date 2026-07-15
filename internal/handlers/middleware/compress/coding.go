package compress

import "strings"

// isGzipName распознаёт gzip и его синоним x-gzip в имени кодировки.
func isGzipName(name string) bool {
	return strings.EqualFold(name, "gzip") || strings.EqualFold(name, "x-gzip")
}
