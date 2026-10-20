package sbom

import (
	"encoding/json"
	"io"
	"testing"
)

func TestInFormats(t *testing.T) {
	doc := Document{Name: "node", Components: []Component{
		{Name: "nodejs-24", Version: "24.1.0-r0", PURL: "pkg:apk/wolfi/nodejs-24@24.1.0-r0?arch=x86_64", License: "MIT", Supplier: "Wolfi"},
		{Name: "npm", Version: "11.0.0", PURL: "pkg:npm/npm@11.0.0", Checksum: "sha512:abcd"},
	}}
	formats, err := doc.InFormats(CycloneDXMediaType, SPDXMediaType, "application/vnd.syft+json")
	if err != nil {
		t.Fatal(err)
	}
	if len(formats) != 2 {
		t.Fatalf("got %d formats, want 2 (unsupported types are skipped)", len(formats))
	}

	for _, f := range formats {
		raw, _ := io.ReadAll(f.Content)
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("%s is not JSON: %v", f.Extension, err)
		}
		switch f.Extension {
		case "cdx.json":
			comps := parsed["components"].([]any)
			if parsed["specVersion"] != "1.5" || len(comps) != 2 {
				t.Errorf("unexpected CycloneDX: %s", raw)
			}
			if comps[0].(map[string]any)["name"] != "nodejs-24" {
				t.Error("components should be sorted by name")
			}
		case "spdx.json":
			if parsed["spdxVersion"] != "SPDX-2.3" || len(parsed["packages"].([]any)) != 2 {
				t.Errorf("unexpected SPDX: %s", raw)
			}
		}
	}

	// Identical content gives identical documents (reproducible layers).
	a, _ := doc.InFormats(SPDXMediaType)
	b, _ := doc.InFormats(SPDXMediaType)
	ra, _ := io.ReadAll(a[0].Content)
	rb, _ := io.ReadAll(b[0].Content)
	if string(ra) != string(rb) {
		t.Error("SPDX output is not deterministic")
	}
}
