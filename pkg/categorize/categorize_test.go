package categorize

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeAsker answers from a table and records what it was asked.
type fakeAsker struct {
	answers map[string]map[string]float64
	err     error
	reqs    []Request
}

func (f *fakeAsker) Ask(_ context.Context, req Request) (map[string]map[string]float64, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]map[string]float64{}
	for _, item := range req.Items {
		if a, ok := f.answers[item.Name]; ok {
			out[item.Name] = a
		}
	}
	return out, nil
}

func closedConfig() *Config {
	return &Config{Model: "m", Mode: ModeClosed, Categories: DefaultCategories, MinWeight: 0.5}
}

func TestLoad(t *testing.T) {
	write := func(body string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ConfigName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	if cfg, err := Load(t.TempDir()); cfg != nil || err != nil {
		t.Fatalf("no config: got %v, %v", cfg, err)
	}
	cfg, err := Load(write("model: some/model\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{BaseURL: defaultBaseURL, Model: "some/model", Mode: ModeClosed, Categories: DefaultCategories, MinWeight: 0.5}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("defaults: got %+v, want %+v", cfg, want)
	}
	for name, body := range map[string]string{
		"no model":     "mode: open\n",
		"bad mode":     "model: m\nmode: ajar\n",
		"bad weight":   "model: m\nmin_weight: 2\n",
		"bad category": "model: m\ncategories: [\"two\\nlines\"]\n",
	} {
		if _, err := Load(write(body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestResolveClosed(t *testing.T) {
	asker := &fakeAsker{answers: map[string]map[string]float64{
		// Matched whatever the case; the unknown category is dropped.
		"chrome": {"browsers": 0.6, "Web": 0.4},
		// Nothing reaches min_weight after normalising.
		"swiss": {"Utilities": 0.4, "Development": 0.3, "Security": 0.3},
		// Only an unknown category.
		"odd": {"Gadgets": 1},
		// An even split is no decision.
		"tie": {"Media": 0.5, "Utilities": 0.5},
	}}
	items := []Item{
		{Name: "chrome", DisplayName: "Chrome"},
		{Name: "swiss"},
		{Name: "odd"},
		{Name: "tie"},
		{Name: "missing"},
		{Name: "slack", Category: "Chat"},
		{Name: "chrome", DisplayName: "Chrome"}, // the same item in a second catalog
	}
	res, err := Resolve(t.Context(), closedConfig(), asker, Lock{}, items)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Lock.Items["chrome"]; got.Category != "Browsers" || got.Weights["Browsers"] != 1 {
		t.Errorf("chrome: %+v", got)
	}
	if got := res.Lock.Items["swiss"]; got.Category != "" || got.Weights["Utilities"] != 0.4 {
		t.Errorf("swiss: %+v", got)
	}
	if got := res.Lock.Items["odd"]; got.Category != "" || got.Fingerprint == "" {
		t.Errorf("odd: %+v", got)
	}
	if _, ok := res.Lock.Items["slack"]; ok {
		t.Error("a hand-written category was locked")
	}
	if res.Asked != 4 || !reflect.DeepEqual(res.Pending, []string{"missing"}) || !reflect.DeepEqual(res.Undecided, []string{"odd", "swiss", "tie"}) {
		t.Errorf("asked %d, pending %v, undecided %v", res.Asked, res.Pending, res.Undecided)
	}
	req := asker.reqs[0]
	if len(asker.reqs) != 1 || len(req.Items) != 5 || req.Open {
		t.Errorf("requests: %+v", asker.reqs)
	}
	// The hand-written category is known and its item is an example.
	if !strings.Contains(strings.Join(req.Known, ","), "Chat") || len(req.Examples) != 1 {
		t.Errorf("known %v, examples %v", req.Known, req.Examples)
	}
}

func TestResolveReusesLock(t *testing.T) {
	cfg := closedConfig()
	items := []Item{{Name: "chrome", Description: "A browser"}, {Name: "vlc"}}
	first := &fakeAsker{answers: map[string]map[string]float64{"chrome": {"Browsers": 1}, "vlc": {"Media": 1}}}
	res, err := Resolve(t.Context(), cfg, first, Lock{}, items)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing changed: nothing is asked, with or without an asker.
	again := &fakeAsker{}
	for _, asker := range []Asker{again, nil} {
		got, reuseErr := Resolve(t.Context(), cfg, asker, res.Lock, items)
		if reuseErr != nil || !reflect.DeepEqual(got.Lock, res.Lock) || len(got.Pending) != 0 {
			t.Errorf("reuse: %+v, %v", got, reuseErr)
		}
	}
	if len(again.reqs) != 0 {
		t.Errorf("asked again: %+v", again.reqs)
	}

	// A changed description is asked about again; without an asker the item
	// is pending and keeps no stale answer.
	items[0].Description = "A web browser by Google"
	changed := &fakeAsker{answers: map[string]map[string]float64{"chrome": {"Browsers": 1}}}
	if _, err = Resolve(t.Context(), cfg, changed, res.Lock, items); err != nil || len(changed.reqs) != 1 || len(changed.reqs[0].Items) != 1 {
		t.Errorf("changed item: %+v, %v", changed.reqs, err)
	}
	offline, _ := Resolve(t.Context(), cfg, nil, res.Lock, items)
	if _, ok := offline.Lock.Items["chrome"]; ok || !reflect.DeepEqual(offline.Pending, []string{"chrome"}) {
		t.Errorf("offline: %+v", offline)
	}

	// A category taken off a closed list is asked about again.
	cfg.Categories = []string{"Browsers"}
	narrowed := &fakeAsker{}
	items[0].Description = "A browser"
	if _, err = Resolve(t.Context(), cfg, narrowed, res.Lock, items); err != nil || len(narrowed.reqs) != 1 || narrowed.reqs[0].Items[0].Name != "vlc" {
		t.Errorf("narrowed list: %+v, %v", narrowed.reqs, err)
	}
}

func TestResolveOpen(t *testing.T) {
	cfg := closedConfig()
	cfg.Mode = ModeOpen
	asker := &fakeAsker{answers: map[string]map[string]float64{
		// A new category that clearly wins is coined.
		"figma": {"Design": 0.9, "Productivity": 0.1},
		// An even split goes to the existing one: the new one is scaled down.
		"sketch": {"Graphics": 0.5, "Productivity": 0.5},
		// Not a category name.
		"long": {strings.Repeat("x", maxCategoryLen+1): 1},
	}}
	res, err := Resolve(t.Context(), cfg, asker, Lock{}, []Item{{Name: "figma"}, {Name: "sketch"}, {Name: "long"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Lock.Items["figma"].Category; got != "Design" {
		t.Errorf("figma: %q", got)
	}
	if got := res.Lock.Items["sketch"]; got.Category != "Productivity" || got.Weights["Graphics"] >= got.Weights["Productivity"] {
		t.Errorf("sketch: %+v", got)
	}
	if got := res.Lock.Items["long"]; got.Category != "" {
		t.Errorf("long: %+v", got)
	}
	if !asker.reqs[0].Open {
		t.Error("request not open")
	}

	// The coined category is known on the next run, in the model's spelling.
	next := &fakeAsker{answers: map[string]map[string]float64{"canva": {"design": 1}}}
	res, err = Resolve(t.Context(), cfg, next, res.Lock, []Item{{Name: "figma"}, {Name: "canva"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Lock.Items["canva"]; got.Category != "Design" || got.Weights["Design"] != 1 {
		t.Errorf("canva: %+v", got)
	}
	if !strings.Contains(strings.Join(next.reqs[0].Known, ","), "Design") {
		t.Errorf("known: %v", next.reqs[0].Known)
	}
}

func TestResolveKeepsAnswersBeforeAFailure(t *testing.T) {
	asker := &fakeAsker{err: errors.New("boom")}
	old := Lock{Items: map[string]Entry{"kept": {Category: "Media", Fingerprint: Item{Name: "kept"}.fingerprint()}}}
	res, err := Resolve(t.Context(), closedConfig(), asker, old, []Item{{Name: "kept"}, {Name: "new"}})
	if err == nil || res.Lock.Items["kept"].Category != "Media" || !reflect.DeepEqual(res.Pending, []string{"new"}) {
		t.Errorf("got %+v, %v", res, err)
	}
}

func TestLockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if lock, err := ReadLock(dir); err != nil || lock.Items != nil {
		t.Fatalf("missing lock: %+v, %v", lock, err)
	}
	lock := Lock{Items: map[string]Entry{"chrome": {Category: "Browsers", Weights: map[string]float64{"Browsers": 0.8, "Utilities": 0.2}, Fingerprint: "abc"}}}
	if err := WriteLock(dir, lock); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLock(dir)
	if err != nil || !reflect.DeepEqual(got, lock) {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestClientAsk(t *testing.T) {
	var body struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
	}
	var auth, path string
	answer := "Here you go:\n```json\n" + `{"items":[{"name":"chrome","categories":[{"name":"Browsers","weight":0.9},{"name":"Utilities","weight":0.1}]}]}` + "\n```"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": chatMessage{"assistant", answer}}}})
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL + "/v1/", Model: "some/model", APIKey: "secret"}
	got, err := client.Ask(t.Context(), Request{
		Known:    []string{"Browsers", "Utilities"},
		Examples: []Item{{Name: "firefox", Category: "Browsers"}},
		Items:    []Item{{Name: "chrome", Description: "A browser"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]map[string]float64{"chrome": {"Browsers": 0.9, "Utilities": 0.1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if auth != "Bearer secret" || path != "/v1/chat/completions" || body.Model != "some/model" {
		t.Errorf("request: auth %q, path %q, model %q", auth, path, body.Model)
	}
	prompt := body.Messages[1].Content
	for _, want := range []string{"Categories: Browsers, Utilities", "Use only these", `"name":"firefox"`, `"description":"A browser"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func TestClientAskErrors(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"status": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no credit", http.StatusPaymentRequired)
		},
		"not a completion": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) },
		"no json": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": chatMessage{"assistant", "I cannot"}}}})
		},
	} {
		server := httptest.NewServer(handler)
		client := &Client{BaseURL: server.URL, Model: "m"}
		if _, err := client.Ask(t.Context(), Request{Items: []Item{{Name: "x"}}}); err == nil {
			t.Errorf("%s: no error", name)
		}
		server.Close()
	}
}
