// Command ghcr-cleanup deletes dangling versions of GHCR container packages
// without breaking the images that are still in use.
//
// Generic "delete untagged" cleanups break multi-architecture images: a tag
// points at an index whose per-architecture manifests are untagged, and
// attestations, signatures and SBOMs refer to images through "subject"
// without tags of their own. This tool deletes an untagged version only when
// nothing kept reaches it (see MakePlan).
//
//	GITHUB_TOKEN=... go run ./tools/ghcr-cleanup -owner taha2samy-3            # dry run
//	GITHUB_TOKEN=... go run ./tools/ghcr-cleanup -owner taha2samy-3 -delete    # delete
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	owner := flag.String("owner", os.Getenv("GITHUB_REPOSITORY_OWNER"), "user or organisation that owns the packages")
	prefix := flag.String("prefix", "fck-fips/", "only packages whose name starts with this")
	grace := flag.Duration("grace", 72*time.Hour, "never delete versions younger than this")
	maxDelete := flag.Int("max-delete", 200, "refuse to run if more versions than this would be deleted")
	doDelete := flag.Bool("delete", false, "delete; without it, only report what would be deleted")
	flag.Parse()

	token := os.Getenv("GITHUB_TOKEN")
	if *owner == "" || token == "" {
		fmt.Fprintln(os.Stderr, "ghcr-cleanup: -owner and GITHUB_TOKEN are required")
		os.Exit(2)
	}

	var report strings.Builder
	err := run(context.Background(), &report, config{
		owner:     strings.ToLower(*owner),
		prefix:    *prefix,
		grace:     *grace,
		maxDelete: *maxDelete,
		delete:    *doDelete,
		github:    &GitHub{API: "https://api.github.com", Token: token, HTTP: &http.Client{Timeout: time.Minute}},
		registry:  &Registry{Host: "https://ghcr.io", User: os.Getenv("GITHUB_ACTOR"), Password: token, HTTP: &http.Client{Timeout: time.Minute}},
		now:       time.Now(),
	})
	fmt.Print(report.String())
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if f, ferr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); ferr == nil {
			fmt.Fprint(f, report.String())
			f.Close()
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ghcr-cleanup:", err)
		os.Exit(1)
	}
}

type config struct {
	owner, prefix string
	grace         time.Duration
	maxDelete     int
	delete        bool
	github        *GitHub
	registry      *Registry
	now           time.Time
}

func run(ctx context.Context, w io.Writer, c config) error {
	if err := c.github.Init(ctx, c.owner); err != nil {
		return err
	}
	pkgs, err := c.github.Packages(ctx, c.prefix)
	if err != nil {
		return err
	}

	mode := "dry run"
	if c.delete {
		mode = "deleting"
	}
	fmt.Fprintf(w, "## GHCR cleanup (%s, grace %s)\n\n", mode, c.grace)

	// Plan everything first: no deletion happens unless every package plans.
	type planned struct {
		pkg  string
		plan Plan
		n    int
	}
	var plans []planned
	total := 0
	for _, pkg := range pkgs {
		versions, err := c.github.Versions(ctx, pkg)
		if err != nil {
			return err
		}
		repo := c.owner + "/" + pkg
		plan, err := MakePlan(versions, func(d string) (Manifest, error) { return c.registry.Manifest(ctx, repo, d) }, c.now, c.grace)
		if err != nil {
			return fmt.Errorf("%s: %w (nothing deleted)", pkg, err)
		}
		plans = append(plans, planned{pkg, plan, len(versions)})
		total += len(plan.Delete)
	}
	if total > c.maxDelete {
		return fmt.Errorf("%d versions would be deleted, more than -max-delete %d; nothing deleted", total, c.maxDelete)
	}

	fmt.Fprintln(w, "| Package | Versions | Kept | Dangling |")
	fmt.Fprintln(w, "|---|---:|---:|---:|")
	for _, p := range plans {
		fmt.Fprintf(w, "| `%s` | %d | %d | %d |\n", p.pkg, p.n, p.n-len(p.plan.Delete), len(p.plan.Delete))
	}
	fmt.Fprintln(w)

	var failed int
	for _, p := range plans {
		for _, v := range p.plan.Delete {
			age := c.now.Sub(v.Created).Round(time.Hour)
			if !c.delete {
				fmt.Fprintf(w, "- would delete `%s@%s` (untagged, unreferenced, %s old)\n", p.pkg, short(v.Digest), age)
				continue
			}
			if err := c.github.Delete(ctx, p.pkg, v.ID); err != nil {
				failed++
				fmt.Fprintf(w, "- **failed** `%s@%s`: %v\n", p.pkg, short(v.Digest), err)
				continue
			}
			fmt.Fprintf(w, "- deleted `%s@%s` (%s old)\n", p.pkg, short(v.Digest), age)
		}
	}
	if total == 0 {
		fmt.Fprintln(w, "Nothing dangling.")
	}
	if failed > 0 {
		return fmt.Errorf("%d deletions failed", failed)
	}
	return nil
}
