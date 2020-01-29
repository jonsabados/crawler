package core

import (
	"context"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func Test_ParseLinks(t *testing.T) {
	testCases := []struct{
		desc string
		input string
		expectedResult []string
	}{
		{
			"valid html",
			"testresources/valid.html",
			[]string{
				"http://foo.bar.com/icky_whitespace",
				"http://foo.bar.com/nice_link",
				"http://foo.bar.com/CasingFun",
				"mailto:someemail@foo.bar.com",
				"http://foo.bar.com/img",
			},
		},
		{
			"invalid html",
			"testresources/invalid.html",
			[]string{
				"http://foo.bar.com/icky_whitespace",
				"http://foo.bar.com/nice_link",
				"http://foo.bar.com/CasingFun",
				"mailto:someemail@foo.bar.com",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			asserter := assert.New(t)
			in, err := os.Open(tc.input)
			if asserter.NoError(err) {
				defer in.Close()
				res := ParseLinks(context.Background(), in)
				asserter.Equal(tc.expectedResult, res)
			}
		})
	}
}