package labels

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxLength = 64

func Normalize(value string) (string, error) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > MaxLength {
		return "", fmt.Errorf("label must be %d characters or fewer", MaxLength)
	}
	return value, nil
}
