package openstax

import (
	"encoding/json"
	"testing"
)

func TestArchiveBookAcceptsNumericRepoSchemaVersion(t *testing.T) {
	var book ArchiveBook
	err := json.Unmarshal([]byte(`{"title":"Example","repo_schema_version":1,"tree":{}}`), &book)
	if err != nil {
		t.Fatalf("numeric repo_schema_version should decode: %v", err)
	}
}
