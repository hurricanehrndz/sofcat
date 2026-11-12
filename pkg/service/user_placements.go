package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// UserPlacement is one entry of the user-placement plan, listUserPlacements'
// result: a zip item's variant for a placement whose scope on this OS is
// user. The service never writes these trees; the per-user helper
// (`<slug>-useragent.exe`) does, from the archive the service staged.
type UserPlacement struct {
	Item      string `json:"item"`
	Version   string `json:"version"`
	Placement string `json:"placement"`
	// Path is the placement root from placements.yaml, unexpanded: the helper
	// expands the home variable (placement.ProfileVar) for its own user.
	Path string `json:"path"`
	// Archive is the absolute path of the staged copy under
	// UserArtifactsDir, named <hash>.zip. Hash is its sha256, TreeHash its
	// contents'.
	Archive  string `json:"archive"`
	Hash     string `json:"hash"`
	TreeHash string `json:"treeHash"`
	// Held is true for an item under a conditional_items block the service
	// could not decide, one deferred this run, or one whose archive failed
	// to stage: the helper neither installs nor removes it.
	Held bool `json:"held"`
	// Managed is true when a manifest lists the item in managed_installs:
	// the helper's receipt records it and the tree's templates read it as
	// .Item.Managed, so a flip is detected in user scope as in machine scope.
	Managed bool `json:"managed"`

	// location is the variant's repository path, for staging.
	location string
}

// UserPlacements is the user-placement plan as listUserPlacements serves it
// and as a run stores it: the entries, and the facts that run collected,
// once, which the helper renders user-scope tree templates with. The helper
// runs no scripts; whatever an admin script prints reaches every logged-on
// user's helper (conditions guide, "Admin-provided facts").
type UserPlacements struct {
	Entries []UserPlacement  `json:"entries"`
	Facts   conditions.Facts `json:"facts"`
	// Placements holds the spec of every placement whose scope on this OS is
	// user, entries or not, so the helper enforces exclusive and places
	// items_dir, vars and files as the service would, and still removes a
	// placement's files once its last item leaves. A plan from an older
	// service has none: nil, never empty, so the helper can tell.
	Placements map[string]placement.Spec `json:"placements"`
	// UserFiles holds a UserFileSet for each machine placement with a user-scope
	// files entry. A placement whose root cannot be resolved is held, so the
	// helper leaves its keys alone. Never nil when stored.
	UserFiles map[string]UserFileSet `json:"userFiles"`
}

// UserFileSet is what the per-user helper writes for one machine placement
// with a files entry whose scope is user: the placement's whole spec (the
// helper renders only the entries that are user scope), its expanded root and
// its items as the service's receipts describe them, sorted by name. Items
// is empty, not nil, when nothing is placed, so the helper still removes the
// keys. Held is set when the service could not resolve the root or read the
// receipts: the helper then neither renders nor removes what it merged.
type UserFileSet struct {
	Spec  placement.Spec `json:"spec"`
	Root  string         `json:"root"`
	Items []render.Item  `json:"items"`
	Held  bool           `json:"held"`
}

// UserPlacementResult is the helper's outcome for one placement, sent with
// reportUserPlacements. Outcome is an installer.Outcome.
type UserPlacementResult struct {
	Item      string `json:"item"`
	Placement string `json:"placement"`
	Outcome   string `json:"outcome"`
	Error     string `json:"error,omitempty"`
}

type reportUserPlacementsParams struct {
	Results []UserPlacementResult `json:"results"`
}

type reportUserPlacementsResponse struct{}

// UserArtifactsDir is where the service stages user-placement archives
// (identity.UserArtifactsDirFor): user-artifacts inside the data directory
// on Windows, the one entry of it whose ACL lets Users in, and beside it on
// macOS. Administrators write it and users read it. It is not a placement;
// the guard refuses placements into it as it does the data directory.
// Tests point it at a temporary directory.
var UserArtifactsDir = func(dataDir string) string {
	return identity.UserArtifactsDirFor(runtime.GOOS, dataDir)
}

// UserPlan builds the user-placement plan from a run's desired set (the
// names it installs or updates, closed over dependencies and update_for
// riders) and its held set (names under conditional_items blocks it could not
// decide), so the plan never diverges from what the run acted on. In v1 every
// user gets the same plan: those items' zip variants whose placement scope on
// goos is user. Held names outside the desired set are listed with Held set;
// managed holds the names the manifests list in managed_installs. Entries
// are sorted by item, then placement in table order. Each archive is named
// <hash>.zip inside dir, so two variants share a file only when they are the
// same archive and a catalog location never shapes a path; a variant whose
// hash is not a sha256 is left out.
func UserPlan(desired, held, managed map[string]bool, catalogs map[int]map[string]catalog.Item, table placement.Table, goos, dir string) []UserPlacement {
	userPlacements := userScopeNames(table, goos)

	names := slices.Collect(maps.Keys(desired))
	for name := range held {
		if !desired[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	entries := []UserPlacement{}
	for _, name := range names {
		item, _, ok, _ := firstCatalogItem(name, catalogs, nil)
		if !ok || item.Installer.Type != "zip" {
			continue
		}
		for _, p := range userPlacements {
			v, ok := item.Installer.Variants[p][goos]
			if !ok {
				continue
			}
			hash := strings.ToLower(v.Hash)
			if sum, err := hex.DecodeString(hash); err != nil || len(sum) != sha256.Size {
				slog.Warn("user-placement variant has no valid sha256, skipping", "item", name, "placement", p)
				continue
			}
			root, _ := table.Placements[p].Path(goos)
			entries = append(entries, UserPlacement{
				Item:      name,
				Version:   item.Version,
				Placement: p,
				Path:      root,
				Archive:   filepath.Join(dir, hash+".zip"),
				Hash:      hash,
				TreeHash:  v.TreeHash,
				Held:      !desired[name],
				Managed:   managed[name],
				location:  v.Location,
			})
		}
	}
	return entries
}

// userScopeNames returns the placements whose scope on goos is user, in
// table order.
func userScopeNames(table placement.Table, goos string) []string {
	var names []string
	for _, name := range table.Names() {
		if table.Placements[name].ScopeFor(goos) == placement.ScopeUser {
			names = append(names, name)
		}
	}
	return names
}

// UserSpecs returns the spec of every placement whose scope on goos is
// user, for UserPlacements.Placements.
func UserSpecs(table placement.Table, goos string) map[string]placement.Spec {
	specs := map[string]placement.Spec{}
	for _, name := range userScopeNames(table, goos) {
		specs[name] = table.Placements[name]
	}
	return specs
}

// UserFileSets returns the UserFileSet of every machine placement on goos with
// a files entry that has a user-scope path there, from zip's receipts. A
// placement whose root or receipts cannot be read is held, with a warning.
func UserFileSets(zip *installer.ZipInstaller, table placement.Table, goos string) map[string]UserFileSet {
	sets := map[string]UserFileSet{}
	for _, name := range table.Names() {
		spec := table.Placements[name]
		if spec.ScopeFor(goos) != placement.ScopeMachine || !slices.ContainsFunc(spec.Files, func(f placement.File) bool {
			return f.Scope == placement.ScopeUser && f.Paths[goos] != ""
		}) {
			continue
		}
		root, items, err := zip.UserFileSet(name, spec)
		if err != nil {
			slog.Warn("unable to describe the user-scope files of a placement; holding them", "placement", name, "err", err)
			sets[name] = UserFileSet{Spec: spec, Items: []render.Item{}, Held: true}
			continue
		}
		sets[name] = UserFileSet{Spec: spec, Root: root, Items: items}
	}
	return sets
}

// UserPlanPath is where a run stores the plan listUserPlacements serves.
func UserPlanPath(appDataPath string) string {
	return filepath.Join(appDataPath, "user-plan.json")
}

// SaveUserPlan stores plan at UserPlanPath through a temporary file, so a
// concurrent listUserPlacements reads either the old plan or the new one.
// json.Marshal sorts map keys and the entries are sorted, so an unchanged
// plan encodes the same bytes and is not rewritten.
func SaveUserPlan(appDataPath string, plan UserPlacements) error {
	if plan.Entries == nil {
		plan.Entries = []UserPlacement{}
	}
	if plan.Facts == nil {
		plan.Facts = conditions.Facts{}
	}
	if plan.Placements == nil {
		plan.Placements = map[string]placement.Spec{}
	}
	if plan.UserFiles == nil {
		plan.UserFiles = map[string]UserFileSet{}
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(UserPlanPath(appDataPath)); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(appDataPath, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(UserPlanPath(appDataPath), 0o644, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// userSyncStartedPath holds the sha256 of the stored plan the per-user
// helpers were last started for.
func userSyncStartedPath(appDataPath string) string {
	return UserPlanPath(appDataPath) + ".started"
}

// userSyncDue reports whether the stored plan differs from the one the
// helpers were last started for, and returns its sha256 for
// markUserSyncStarted. It compares files, not what this run saved, so a
// failed start, a manual run without SYSTEM rights or a service stopped
// between the save and the start is retried at the next run. With no stored
// plan there is nothing to hand out.
func userSyncDue(appDataPath string) (string, bool) {
	data, err := os.ReadFile(UserPlanPath(appDataPath))
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	started, err := os.ReadFile(userSyncStartedPath(appDataPath))
	return hash, err != nil || string(started) != hash
}

// markUserSyncStarted records hash as the plan the helpers were started for.
func markUserSyncStarted(appDataPath, hash string) error {
	return os.WriteFile(userSyncStartedPath(appDataPath), []byte(hash), 0o644)
}

// LoadUserPlan reads the plan the last run stored; before the first run there
// is none, which is an empty plan. It reads no repository, so the helper gets
// its plan at logon even while the repository is unreachable. A stored plan
// from an older service keeps Placements nil.
func LoadUserPlan(appDataPath string) (UserPlacements, error) {
	empty := UserPlacements{Entries: []UserPlacement{}, Facts: conditions.Facts{}, Placements: map[string]placement.Spec{}, UserFiles: map[string]UserFileSet{}}
	data, err := os.ReadFile(UserPlanPath(appDataPath))
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return UserPlacements{}, err
	}
	var stored UserPlacements
	if err := json.Unmarshal(data, &stored); err != nil {
		return UserPlacements{}, fmt.Errorf("decode %s: %w", UserPlanPath(appDataPath), err)
	}
	if stored.Entries == nil {
		stored.Entries = empty.Entries
	}
	if stored.Facts == nil {
		stored.Facts = empty.Facts
	}
	if stored.UserFiles == nil {
		stored.UserFiles = empty.UserFiles
	}
	return stored, nil
}

// StageUserArtifacts makes dir hold exactly the archives plan references:
// each non-held entry's archive is downloaded and verified through zip (its
// cache and repository) and copied in when missing or different, and files
// no entry references are removed. A failure is logged and sets the entry's
// Held in plan, in place, so the helper keeps the user's current tree rather
// than removing it over a download that may work next run; the service never
// writes the user placements themselves.
func StageUserArtifacts(ctx context.Context, zip *installer.ZipInstaller, plan []UserPlacement, dir string) {
	keep := map[string]bool{}
	for _, e := range plan {
		keep[filepath.Base(e.Archive)] = true
	}
	if _, err := os.Stat(dir); len(plan) == 0 && err != nil {
		// Nothing to stage and nothing staged: a build without user
		// placements never creates the directory.
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("unable to create the user-artifacts directory", "path", dir, "err", err)
		return
	}
	for i := range plan {
		e := &plan[i]
		if e.Held {
			continue
		}
		// Verify logs a warning for a missing file, which a first run has.
		if _, err := os.Stat(e.Archive); err == nil && download.Verify(e.Archive, e.Hash) {
			continue
		}
		t := installer.Target{Placement: e.Placement, Variant: catalog.Variant{Location: e.location, Hash: e.Hash, TreeHash: e.TreeHash}}
		cached, err := zip.Fetch(ctx, t, nil)
		if err == nil {
			err = copyFileAtomic(cached, e.Archive)
		}
		if err != nil {
			slog.Warn("unable to stage user-placement archive; holding the item", "item", e.Item, "placement", e.Placement, "err", err)
			e.Held = true
			continue
		}
		slog.Info("Staged user-placement archive", "item", e.Item, "placement", e.Placement, "path", e.Archive)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("unable to list the user-artifacts directory", "path", dir, "err", err)
		return
	}
	for _, entry := range entries {
		if keep[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			slog.Warn("unable to remove unreferenced user-placement archive", "path", entry.Name(), "err", err)
			continue
		}
		slog.Info("Removed unreferenced user-placement archive", "path", entry.Name())
	}
}

// logUserPlacementResults records the helper's results in the service log,
// with the caller the connection resolved: failures at warn, the rest at
// info. recordUserPlacementStatus keeps what the storefront needs.
func logUserPlacementResults(who peer, results []UserPlacementResult) {
	for _, r := range results {
		level := slog.LevelInfo
		if r.Error != "" {
			level = slog.LevelWarn
		}
		slog.Log(context.Background(), level, "user placement result",
			"item", r.Item, "placement", r.Placement, "outcome", r.Outcome, "err", r.Error,
			"requestedBy", who.Name, "requestedByID", who.ID)
	}
}

// copyFileAtomic copies src to dst through a temporary file beside dst, so a
// helper reading dst never sees a partial archive.
func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	// Users read the staged archives; CreateTemp's 0600 would hide them off
	// Windows, where no inherited ACL applies.
	return writeFileAtomic(dst, 0o644, func(w io.Writer) error {
		_, err := io.Copy(w, in)
		return err
	})
}

// writeFileAtomic writes dst through a temporary file beside it, renamed into
// place once complete, with perm. Off Windows perm is the file's access; on
// Windows the directory's inherited ACL is.
func writeFileAtomic(dst string, perm os.FileMode, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".staging-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// dispatchUserPlacements answers the helper's two methods.
func (sr *serviceRunner) dispatchUserPlacements(conn clientConn, req rpcRequest, logger *slog.Logger) (any, *Error) {
	if req.Method == methodListUserPlacements {
		// Like getBranding it skips the queue: the helper runs at logon,
		// often while the startup run is under way, and must not wait it
		// out. It serves the plan the last run stored.
		resp, err := sr.executeCommandSafe(Command{Action: actionListUserPlacements})
		if err != nil {
			return nil, commandError(err, "")
		}
		return resp.UserPlan, nil
	}
	params, err := decodeParams[reportUserPlacementsParams](req.Params)
	if err != nil || params.Results == nil {
		return nil, newError(codeInvalidParams, req.Method+" requires params.results", "")
	}
	who, _ := requester(conn, logger)
	logUserPlacementResults(who, params.Results)
	if err := recordUserPlacementStatus(sr.cfg.AppDataPath, who.ID, params.Results, time.Now().UTC()); err != nil {
		logger.Warn("unable to store user placement status", "err", err)
	}
	return reportUserPlacementsResponse{}, nil
}

// sendUserPlacementCommand is `-S ListUserPlacements`, one JSON line per
// entry and then one holding the plan's placements and user files, and `-S ReportUserPlacements:item,placement,outcome[,error]`, which
// sends one result.
func sendUserPlacementCommand(ctx context.Context, client *Client, cmd Command) (CommandResponse, error) {
	if cmd.Action == actionReportUserPlacements {
		result := UserPlacementResult{Item: cmd.Items[0], Placement: cmd.Items[1], Outcome: cmd.Items[2]}
		if len(cmd.Items) == 4 {
			result.Error = cmd.Items[3]
		}
		if err := client.ReportUserPlacements(ctx, []UserPlacementResult{result}); err != nil {
			return CommandResponse{}, err
		}
		return CommandResponse{Status: "ok", Message: "user placement result reported"}, nil
	}
	plan, err := client.ListUserPlacements(ctx)
	if err != nil {
		return CommandResponse{}, err
	}
	specs, err := json.Marshal(struct {
		Placements map[string]placement.Spec `json:"placements"`
		UserFiles  map[string]UserFileSet    `json:"userFiles"`
	}{plan.Placements, plan.UserFiles})
	if err != nil {
		return CommandResponse{}, fmt.Errorf("failed to encode user placement specs: %w", err)
	}
	resp := CommandResponse{Status: "ok", UserPlan: &plan, Items: make([]string, 0, len(plan.Entries)+1)}
	for _, e := range plan.Entries {
		line, err := json.Marshal(e)
		if err != nil {
			return CommandResponse{}, fmt.Errorf("failed to encode user placement %q: %w", e.Item, err)
		}
		resp.Items = append(resp.Items, string(line))
	}
	resp.Items = append(resp.Items, string(specs))
	return resp, nil
}
