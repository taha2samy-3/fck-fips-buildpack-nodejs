package keys

import "testing"

func TestEmbeddedKeysParse(t *testing.T) {
	if len(WolfiKeyring()) != 1 || len(FIPSKeyring()) != 1 {
		t.Fatal("expected one key in each keyring")
	}
	if _, ok := WolfiKeyring()[WolfiName]; !ok {
		t.Error("Wolfi key not registered under its file name")
	}
	if _, ok := FIPSKeyring()[FIPSName]; !ok {
		t.Error("FIPS key not registered under its file name")
	}
}
