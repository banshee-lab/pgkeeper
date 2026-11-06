package domain

import "testing"

func TestRepositoryID(t *testing.T) {
	first := RepositoryID("s3:https://bucket/repo")
	if first == "" || first != RepositoryID("s3:https://bucket/repo") {
		t.Fatal("repository ID must be stable and non-empty")
	}
	if first == RepositoryID("s3:https://bucket/other") {
		t.Fatal("different repository paths must have different IDs")
	}
}
