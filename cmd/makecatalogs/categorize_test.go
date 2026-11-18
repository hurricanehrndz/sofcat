package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/categorize"
)

// modelServer answers every chat completion with answer and counts the calls.
func modelServer(t *testing.T, answer string) (url string, calls *int) {
	t.Helper()
	calls = new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": answer}}},
		})
	}))
	t.Cleanup(server.Close)
	return server.URL, calls
}

const editorInfo = `
item_name: Editor
display_name: Editor
description: A code editor
category: Development
catalog: production
`

func TestCategorize(t *testing.T) {
	repo := writeRepo(t, map[string]string{"Chrome.yaml": chromeInfo, "Editor.yaml": editorInfo})
	url, calls := modelServer(t, `{"items":[{"name":"Chrome","categories":[{"name":"Browsers","weight":1}]}]}`)
	config := "base_url: " + url + "\nmodel: test/model\n"
	if err := os.WriteFile(filepath.Join(repo, categorize.ConfigName), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	catalogs := func() string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(repo, "catalogs", "production.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	// Without the key the catalogs are written and the item is reported.
	t.Setenv(categorize.APIKeyEnv, "")
	var stdout, stderr bytes.Buffer
	if code := run([]string{repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), categorize.APIKeyEnv+" is not set: Chrome") || strings.Contains(catalogs(), "Browsers") || *calls != 0 {
		t.Errorf("no key: calls %d, stderr %s", *calls, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo, categorize.LockName)); err == nil {
		t.Error("an empty lock file was written")
	}

	// With it the model is asked once and its answer is locked.
	t.Setenv(categorize.APIKeyEnv, "secret")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{repo}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Categorized 1 item(s) with test/model") {
		t.Errorf("stdout: %s", stdout.String())
	}
	if got := catalogs(); !strings.Contains(got, "category: Browsers") || !strings.Contains(got, "category: Development") {
		t.Errorf("catalog:\n%s", got)
	}
	lock, err := os.ReadFile(filepath.Join(repo, categorize.LockName))
	if err != nil || !strings.Contains(string(lock), "Chrome:") || strings.Contains(string(lock), "Editor") {
		t.Errorf("lock: %s, %v", lock, err)
	}

	// Later runs, --check and a run without the key reuse the lock.
	t.Setenv(categorize.APIKeyEnv, "")
	for _, args := range [][]string{{repo}, {"--check", repo}} {
		stderr.Reset()
		if code := run(args, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
			t.Errorf("%v: exit %d: %s", args, code, stderr.String())
		}
	}
	after, _ := os.ReadFile(filepath.Join(repo, categorize.LockName))
	if *calls != 1 || !strings.Contains(catalogs(), "category: Browsers") || !bytes.Equal(lock, after) {
		t.Errorf("reuse: calls %d, catalog:\n%s", *calls, catalogs())
	}
}

func TestCategorizeModelFailureStillWritesCatalogs(t *testing.T) {
	repo := writeRepo(t, map[string]string{"Chrome.yaml": chromeInfo})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer server.Close()
	if err := os.WriteFile(filepath.Join(repo, categorize.ConfigName), []byte("base_url: "+server.URL+"\nmodel: m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(categorize.APIKeyEnv, "secret")
	var stdout, stderr bytes.Buffer
	if code := run([]string{repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "502") || !strings.Contains(stderr.String(), "the model gave no answer: Chrome") {
		t.Errorf("stderr: %s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "catalogs", "production.yaml")); err != nil {
		t.Error(err)
	}
}

func TestCategorizeBadConfigFails(t *testing.T) {
	repo := writeRepo(t, map[string]string{"Chrome.yaml": chromeInfo})
	if err := os.WriteFile(filepath.Join(repo, categorize.ConfigName), []byte("mode: open\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{repo}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "model is required") {
		t.Errorf("exit %d: %s", code, stderr.String())
	}
}

// build reads categorize.yaml from the source and keeps the lock there, so
// the wiped output never loses it.
func TestBuildCategorizes(t *testing.T) {
	source := buildSource(t)
	url, calls := modelServer(t, `{"items":[{"name":"Chrome","categories":[{"name":"Browsers","weight":1}]},{"name":"hello","categories":[{"name":"AI","weight":1}]}]}`)
	writeFiles(t, source, map[string]string{categorize.ConfigName: "base_url: " + url + "\nmodel: m\n"})
	t.Setenv(categorize.APIKeyEnv, "secret")
	out := filepath.Join(t.TempDir(), "repo")
	for range 2 {
		if code, stderr := runBuildIn(source, out); code != 0 || stderr != "" {
			t.Fatalf("exit %d: %s", code, stderr)
		}
	}
	if *calls != 1 {
		t.Errorf("model asked %d times", *calls)
	}
	if _, err := os.Stat(filepath.Join(source, categorize.LockName)); err != nil {
		t.Error(err)
	}
	body, err := os.ReadFile(filepath.Join(out, "catalogs", "production.yaml"))
	if err != nil || !strings.Contains(string(body), "category: Browsers") {
		t.Errorf("catalog: %s, %v", body, err)
	}
}
