package models

import "testing"

func TestImageRef(t *testing.T) {
    cases := map[string]string{
        "ghcr.io/o/app":            "ghcr.io/o/app:latest",
        "ghcr.io/o/app:4b91165":    "ghcr.io/o/app:4b91165",
        "registry:5000/app":        "registry:5000/app:latest",
        "registry:5000/app:v1":     "registry:5000/app:v1",
        "app@sha256:abcdef":        "app@sha256:abcdef",
        "nginx":                    "nginx:latest",
    }
    for in, want := range cases {
        if got := ImageRef(in); got != want {
            t.Errorf("ImageRef(%q) = %q, want %q", in, got, want)
        }
    }
}
