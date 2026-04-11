package main

import (
	"testing"
	"github.com/wow-look-at-my/testify/assert"
)

func TestBrowserCommand(t *testing.T) {
	cases := []struct {
		goos	string
		want	string
	}{
		{"darwin", "open"},
		{"windows", "rundll32"},
		{"linux", "xdg-open"},
		{"freebsd", "xdg-open"},
	}
	for _, c := range cases {
		got := browserCommand(c.goos, "http://127.0.0.1:9999/")
		assert.False(t, len(got) == 0 || got[0] != c.want)

		assert.Equal(t, "http://127.0.0.1:9999/", got[len(got)-1])

	}
}
