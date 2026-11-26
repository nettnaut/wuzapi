package main

import (
	"strings"
	"testing"
)

func TestRedactTokenNeverLeaksFullToken(t *testing.T) {
	tok := "wgIvSECRETSECRETSECRET"
	got := redactToken(tok)
	if strings.Contains(got, "SECRET") {
		t.Fatalf("redactToken leaked the token: %q", got)
	}
	if !strings.HasPrefix(got, "wgIv") {
		t.Fatalf("redactToken should keep a 4-char prefix, got %q", got)
	}
	for _, short := range []string{"", "ab", "abcd"} {
		if r := redactToken(short); r != strings.Repeat("*", len(short)) {
			t.Fatalf("redactToken(%q) = %q, want all stars", short, r)
		}
	}
}

func TestRedactS3ConfigMasksCredentialsWithoutMutating(t *testing.T) {
	orig := &S3Config{AccessKey: "AKIASECRETSECRET", SecretKey: "verySECRETSECRET"}
	got := redactS3Config(orig)
	if strings.Contains(got.AccessKey, "SECRET") || strings.Contains(got.SecretKey, "SECRET") {
		t.Fatalf("credentials leaked: %+v", got)
	}
	if orig.SecretKey != "verySECRETSECRET" {
		t.Fatal("redactS3Config mutated its input")
	}
	if redactS3Config(nil) != nil {
		t.Fatal("nil in should be nil out")
	}
}
