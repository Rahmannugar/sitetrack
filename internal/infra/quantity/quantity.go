package quantity

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrInvalid = errors.New("invalid quantity")

func Normalize(value string, positive bool) (string, error) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) < 1 || len(parts[0]) > 15 || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return "", ErrInvalid
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) < 1 || len(fraction) > 3 {
			return "", ErrInvalid
		}
	}
	for _, digit := range parts[0] + fraction {
		if digit < '0' || digit > '9' {
			return "", ErrInvalid
		}
	}
	fraction += strings.Repeat("0", 3-len(fraction))
	milli, err := strconv.ParseInt(parts[0]+fraction, 10, 64)
	if err != nil || (positive && milli == 0) {
		return "", ErrInvalid
	}
	return fmt.Sprintf("%d.%03d", milli/1000, milli%1000), nil
}
