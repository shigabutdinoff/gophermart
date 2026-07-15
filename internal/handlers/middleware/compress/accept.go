package compress

import (
	"strconv"
	"strings"
)

// acceptsGzip разбирает Accept-Encoding, где явный gzip важнее маски *.
func acceptsGzip(header string) bool {
	wildcard := false
	for coding := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(coding, ";")
		name = strings.TrimSpace(name)
		switch {
		case strings.EqualFold(name, "gzip"), strings.EqualFold(name, "x-gzip"):
			return acceptableQ(params)
		case name == "*":
			wildcard = acceptableQ(params)
		}
	}
	return wildcard
}

// acceptableQ читает параметр q, где q=0 означает явный отказ.
func acceptableQ(params string) bool {
	for param := range strings.SplitSeq(params, ";") {
		param = strings.TrimSpace(param)
		if len(param) < 2 || !strings.EqualFold(param[:2], "q=") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64)
		if err != nil || q == 0 {
			return false
		}
	}
	return true
}
