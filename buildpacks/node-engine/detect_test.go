package nodeengine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/paketo-buildpacks/packit/v2"
)

func detect(t *testing.T, dir string) (packit.DetectResult, error) {
	t.Helper()
	return Detect()(packit.DetectContext{WorkingDir: dir})
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectWithoutVersionSources(t *testing.T) {
	result, err := detect(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := packit.BuildPlan{
		Provides: []packit.BuildPlanProvision{{Name: PlanNode}},
		Or: []packit.BuildPlan{{
			Provides: []packit.BuildPlanProvision{{Name: PlanNode}, {Name: PlanNPM}},
		}},
	}
	if !reflect.DeepEqual(result.Plan, want) {
		t.Errorf("plan = %+v", result.Plan)
	}
}

func TestDetectVersionSources(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".nvmrc"), "lts/iron\n")
	write(t, filepath.Join(dir, ".node-version"), "v22.11.0")
	write(t, filepath.Join(dir, "package.json"), `{"engines": {"node": "^24"}}`)
	t.Setenv("BP_NODE_VERSION", "26")

	result, err := detect(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range result.Plan.Requires {
		md := r.Metadata.(map[string]any)
		got[md["version-source"].(string)] = md["version"].(string)
	}
	want := map[string]string{"BP_NODE_VERSION": "26", ".nvmrc": "20", ".node-version": "22.11.0", "package.json": "^24"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requirements = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(result.Plan.Requires, result.Plan.Or[0].Requires) {
		t.Error("both alternatives must carry the same requirements")
	}
}

func TestDetectProjectPath(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "apps/web/.node-version"), "24")
	t.Setenv("BP_NODE_PROJECT_PATH", "apps/web")
	result, err := detect(t, dir)
	if err != nil || len(result.Plan.Requires) != 1 {
		t.Fatalf("plan %+v, err %v", result.Plan, err)
	}

	t.Setenv("BP_NODE_PROJECT_PATH", "missing")
	if _, err := detect(t, dir); err == nil {
		t.Error("expected an error for a missing project path")
	}
}

func TestDetectFIPSRequiresProvider(t *testing.T) {
	t.Setenv("BP_OPENSSL_FIPS", "true")
	result, err := detect(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plan.Requires) != 1 || result.Plan.Requires[0].Name != PlanOpenSSLFIPS {
		t.Errorf("FIPS mode must require openssl-fips: %+v", result.Plan.Requires)
	}

	t.Setenv("BP_OPENSSL_FIPS", "yes please")
	if _, err := detect(t, t.TempDir()); err == nil {
		t.Error("an invalid boolean must fail rather than silently disable FIPS")
	}
}

func TestDetectInvalidVersionFails(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".nvmrc"), "not-a-version")
	if _, err := detect(t, dir); err == nil {
		t.Error("an unparsable .nvmrc should fail detection with a clear error")
	}

	dir = t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{`)
	if _, err := detect(t, dir); err == nil {
		t.Error("a malformed package.json should be reported")
	}
}
