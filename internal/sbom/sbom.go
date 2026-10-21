// Package sbom writes CycloneDX and SPDX documents for the components a
// buildpack installs into a layer. It describes packages the buildpack chose
// and verified itself, so no filesystem scanning is involved.
package sbom

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/paketo-buildpacks/packit/v2"
)

// Media types the CNB spec defines for layer SBOMs.
const (
	CycloneDXMediaType = "application/vnd.cyclonedx+json"
	SPDXMediaType      = "application/spdx+json"
)

// Component is one installed package.
type Component struct {
	Name     string
	Version  string
	PURL     string
	License  string // SPDX expression, may be empty
	Supplier string // e.g. "Wolfi", "npm"
	Checksum string // "sha256:<hex>" or "sha512:<hex>", optional
}

// Document is a set of components installed together.
type Document struct {
	Name       string // layer or buildpack name, used as the SPDX document name
	Components []Component
}

// InFormats renders the document in each requested media type.
func (d Document) InFormats(mediaTypes ...string) (packit.SBOMFormats, error) {
	comps := append([]Component(nil), d.Components...)
	sort.Slice(comps, func(i, j int) bool { return comps[i].Name < comps[j].Name })

	var out packit.SBOMFormats
	for _, mt := range mediaTypes {
		var (
			raw []byte
			ext string
			err error
		)
		switch mt {
		case CycloneDXMediaType:
			raw, err = cycloneDX(comps)
			ext = "cdx.json"
		case SPDXMediaType:
			raw, err = spdx(d.Name, comps)
			ext = "spdx.json"
		default:
			// The buildpack declares its formats in buildpack.toml; anything
			// else is simply not produced.
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, packit.SBOMFormat{Extension: ext, Content: bytes.NewReader(raw)})
	}
	return out, nil
}

func cycloneDX(comps []Component) ([]byte, error) {
	type hashT struct {
		Alg     string `json:"alg"`
		Content string `json:"content"`
	}
	type license struct {
		Expression string `json:"expression"`
	}
	type component struct {
		Type     string            `json:"type"`
		BOMRef   string            `json:"bom-ref"`
		Name     string            `json:"name"`
		Version  string            `json:"version"`
		PURL     string            `json:"purl,omitempty"`
		Supplier map[string]string `json:"supplier,omitempty"`
		Licenses []license         `json:"licenses,omitempty"`
		Hashes   []hashT           `json:"hashes,omitempty"`
	}

	doc := map[string]any{
		"bomFormat":   "CycloneDX",
		"specVersion": "1.5",
		"version":     1,
	}
	list := make([]component, 0, len(comps))
	for _, c := range comps {
		cc := component{
			Type:    "library",
			BOMRef:  ref(c),
			Name:    c.Name,
			Version: c.Version,
			PURL:    c.PURL,
		}
		if c.Supplier != "" {
			cc.Supplier = map[string]string{"name": c.Supplier}
		}
		if c.License != "" {
			cc.Licenses = []license{{Expression: c.License}}
		}
		if algo, sum, ok := strings.Cut(c.Checksum, ":"); ok {
			cc.Hashes = []hashT{{Alg: strings.ToUpper(strings.Replace(algo, "sha", "SHA-", 1)), Content: sum}}
		}
		list = append(list, cc)
	}
	doc["components"] = list
	return json.MarshalIndent(doc, "", "  ")
}

func spdx(name string, comps []Component) ([]byte, error) {
	type ref struct {
		Category string `json:"referenceCategory"`
		Type     string `json:"referenceType"`
		Locator  string `json:"referenceLocator"`
	}
	type checksum struct {
		Algorithm string `json:"algorithm"`
		Value     string `json:"checksumValue"`
	}
	type pkg struct {
		SPDXID           string     `json:"SPDXID"`
		Name             string     `json:"name"`
		Version          string     `json:"versionInfo"`
		Supplier         string     `json:"supplier"`
		DownloadLocation string     `json:"downloadLocation"`
		FilesAnalyzed    bool       `json:"filesAnalyzed"`
		LicenseConcluded string     `json:"licenseConcluded"`
		LicenseDeclared  string     `json:"licenseDeclared"`
		Copyright        string     `json:"copyrightText"`
		Checksums        []checksum `json:"checksums,omitempty"`
		ExternalRefs     []ref      `json:"externalRefs,omitempty"`
	}
	type relationship struct {
		Element string `json:"spdxElementId"`
		Type    string `json:"relationshipType"`
		Related string `json:"relatedSpdxElement"`
	}

	// The namespace must be unique per document; derive it from the content
	// so that identical layers produce identical documents.
	h := sha256.New()
	for _, c := range comps {
		fmt.Fprintf(h, "%s@%s\n", c.Name, c.Version)
	}

	pkgs := make([]pkg, 0, len(comps))
	rels := []relationship{}
	for _, c := range comps {
		id := "SPDXRef-Package-" + spdxID(c.Name+"-"+c.Version)
		lic := c.License
		if lic == "" {
			lic = "NOASSERTION"
		}
		supplier := "NOASSERTION"
		if c.Supplier != "" {
			supplier = "Organization: " + c.Supplier
		}
		p := pkg{
			SPDXID:           id,
			Name:             c.Name,
			Version:          c.Version,
			Supplier:         supplier,
			DownloadLocation: "NOASSERTION",
			LicenseConcluded: "NOASSERTION",
			LicenseDeclared:  lic,
			Copyright:        "NOASSERTION",
		}
		if algo, sum, ok := strings.Cut(c.Checksum, ":"); ok {
			p.Checksums = []checksum{{Algorithm: strings.ToUpper(algo), Value: sum}}
		}
		if c.PURL != "" {
			p.ExternalRefs = []ref{{Category: "PACKAGE-MANAGER", Type: "purl", Locator: c.PURL}}
		}
		pkgs = append(pkgs, p)
		rels = append(rels, relationship{Element: "SPDXRef-DOCUMENT", Type: "DESCRIBES", Related: id})
	}

	doc := map[string]any{
		"spdxVersion":       "SPDX-2.3",
		"dataLicense":       "CC0-1.0",
		"SPDXID":            "SPDXRef-DOCUMENT",
		"name":              name,
		"documentNamespace": "https://github.com/taha2samy-3/fck-fips-buildpack-nodejs/sbom/" + hex.EncodeToString(h.Sum(nil))[:32],
		"creationInfo": map[string]any{
			// A fixed timestamp keeps layers reproducible, the same choice the
			// lifecycle makes for file times.
			"created":  time.Unix(315532801, 0).UTC().Format(time.RFC3339),
			"creators": []string{"Tool: fck-fips-buildpack-nodejs"},
		},
		"packages":      pkgs,
		"relationships": rels,
	}
	return json.MarshalIndent(doc, "", "  ")
}

func ref(c Component) string {
	if c.PURL != "" {
		return c.PURL
	}
	return c.Name + "@" + c.Version
}

// spdxID keeps only the characters SPDX identifiers allow.
func spdxID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
