package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

var ErrInvalidISBN = errors.New("invalid ISBN")

type ISBN struct {
	ISBN10 string
	ISBN13 string
}

// NormalizeISBN accepts ISBN-10 or ISBN-13 with common spacing and dash
// separators. ISBN-10 is converted to its 978-prefixed ISBN-13 equivalent.
func NormalizeISBN(input string) (ISBN, error) {
	var cleaned strings.Builder
	for _, r := range input {
		switch {
		case r >= '0' && r <= '9', r == 'X', r == 'x':
			cleaned.WriteRune(r)
		case unicode.IsSpace(r), unicode.Is(unicode.Pd, r):
			// Human-formatted ISBN separator.
		default:
			return ISBN{}, fmt.Errorf("%w: unsupported character", ErrInvalidISBN)
		}
	}
	value := strings.ToUpper(cleaned.String())
	switch len(value) {
	case 10:
		if !validISBN10(value) {
			return ISBN{}, fmt.Errorf("%w: ISBN-10 checksum", ErrInvalidISBN)
		}
		base := "978" + value[:9]
		return ISBN{ISBN10: value, ISBN13: base + string('0'+rune(isbn13CheckDigit(base)))}, nil
	case 13:
		if !validISBN13(value) {
			return ISBN{}, fmt.Errorf("%w: ISBN-13 checksum", ErrInvalidISBN)
		}
		result := ISBN{ISBN13: value}
		if strings.HasPrefix(value, "978") {
			base := value[3:12]
			result.ISBN10 = base + isbn10CheckCharacter(base)
		}
		return result, nil
	default:
		return ISBN{}, fmt.Errorf("%w: expected 10 or 13 digits", ErrInvalidISBN)
	}
}

func validISBN10(value string) bool {
	if len(value) != 10 {
		return false
	}
	sum := 0
	for i := 0; i < 10; i++ {
		digit := 0
		if i == 9 && value[i] == 'X' {
			digit = 10
		} else if value[i] >= '0' && value[i] <= '9' {
			digit = int(value[i] - '0')
		} else {
			return false
		}
		sum += (10 - i) * digit
	}
	return sum%11 == 0
}

func isbn10CheckCharacter(base string) string {
	sum := 0
	for i := 0; i < 9; i++ {
		sum += (10 - i) * int(base[i]-'0')
	}
	check := (11 - sum%11) % 11
	if check == 10 {
		return "X"
	}
	return string('0' + rune(check))
}

func validISBN13(value string) bool {
	if len(value) != 13 {
		return false
	}
	for i := 0; i < 13; i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return int(value[12]-'0') == isbn13CheckDigit(value[:12])
}

func isbn13CheckDigit(base string) int {
	sum := 0
	for i := 0; i < 12; i++ {
		weight := 1
		if i%2 == 1 {
			weight = 3
		}
		sum += weight * int(base[i]-'0')
	}
	return (10 - sum%10) % 10
}
