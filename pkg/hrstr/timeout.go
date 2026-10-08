package hrstr

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ParseTimeout parses a positive timeout given as integer seconds ("30") or as
// a Go [time.ParseDuration] string ("30s", "1m30s").
func ParseTimeout(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	var d time.Duration
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > math.MaxInt64/int64(time.Second) {
			return 0, fmt.Errorf("timeout %q is too large", s)
		}
		d = time.Duration(n) * time.Second
	} else {
		d, err = time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf(
				"parse timeout %q: expected integer seconds or a Go duration like %q",
				s,
				"30s",
			)
		}
	}
	if d <= 0 {
		return 0, fmt.Errorf("timeout must be positive: %q", s)
	}
	return d, nil
}
