package domain

import (
	"errors"
	"testing"
)

func TestNormalizeISBN(t *testing.T) {
	cases := []struct {
		input  string
		isbn10 string
		isbn13 string
	}{
		{"0-306-40615-2", "0306406152", "9780306406157"},
		{"978 0 306 40615 7", "0306406152", "9780306406157"},
		{"0-8044-2957-x", "080442957X", "9780804429573"},
		{"979-1-23456-789-6", "", "9791234567896"},
		{"978‑1‑60358‑055‑7", "1603580557", "9781603580557"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := NormalizeISBN(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.ISBN10 != tc.isbn10 || got.ISBN13 != tc.isbn13 {
				t.Fatalf("got %+v, want %s / %s", got, tc.isbn10, tc.isbn13)
			}
		})
	}
}

func TestNormalizeISBNRejectsInvalid(t *testing.T) {
	for _, input := range []string{"", "123", "0306406153", "9780306406158", "03064061X2", "978030640615X", "978/0306406157", "abc"} {
		if _, err := NormalizeISBN(input); !errors.Is(err, ErrInvalidISBN) {
			t.Fatalf("%q: expected invalid ISBN, got %v", input, err)
		}
	}
}
