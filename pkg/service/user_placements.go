package service

import (
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
	"slices"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/harness"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
)

// UserPlacement is one entry of the user-placement plan, listUserPlacements'
// result: a zip item's variant for a placement whose scope on this OS is
// user. The service never writes these trees; the per-user helper
// (`<slug>-ui.exe --user-sync`) does, from the archive the service staged.
type UserPlacement struct {
	Item      string `json:"item"`
	Version   string `json:"version"`
	Placement string `json:"placement"`
	// Path is the placement root from harnesses.yaml, unexpanded: the helper
	// expands ${USERPROFILE} for its own user.
	Path string `json:"path"`
	// Archive is the absolute path of the staged copy under
	// UserArtifactsDir, named <hash>.zip. Hash is its sha256, TreeHash its
	// contents'.
	Archive  string `json:"archive"`
	Hash     string `json:"hash"`
	TreeHash string `json:"treeHash"`
	// Held is true for an item under a conditional_items block the service
	// could not decide: the helper neither installs nor removes it.
	Held bool `json:"held"`

	// location is the variant's repository path, for staging.
	location string
}

// UserPlacementResult is the helper's outcome for one placement, sent with
// reportUserPlacements. Outcome is an installer.Outcome.
type UserPlacementResult struct {
	Item      string `json:"item"`
	Placement string `json:"placement"`
	Outcome   string `json:"outcome"`
	Error     string `json:"error,omitempty"`
}

type listUserPlacementsResponse struct {
	Entries []UserPlacement `json:"entries"`
}

type reportUserPlacementsParams struct {
	Results []UserPlacementResult `json:"results"`
}

type reportUserPlacementsResponse struct{}

// UserArtifactsDir is where the service stages user-placement archives:
// user-artifacts beside the running executable, which is
// ${ProgramFiles}\<Name>\user-artifacts for an MSI or bootstrap install.
// Administrators write it and users read it. It is not a placement and is
// not checked against the write roots, which refuse the binary directory.
// Tests point it at a temporary directory.
var UserArtifactsDir = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "user-artifacts"), nil
}

// UserPlan builds the user-placement plan from a run's desired set (the
// names it installs or updates, closed over dependencies and update_for
// riders) and its held set (names under conditional_items blocks it could not
// decide), so the plan never diverges from what the run acted on. In v1 every
// user gets the same plan: those items' zip variants whose placement scope on
// goos is user. Held names outside the desired set are listed with Held set.
// Entries are sorted by item, then placement in table order. Each archive is
// named <hash>.zip inside dir, so two variants share a file only when they
// are the same archive and a catalog location never shapes a path; a variant
// whose hash is not a sha256 is left out.
func UserPlan(desired, held map[string]bool, catalogs map[int]map[string]catalog.Item, table harness.Table, goos, dir string) []UserPlacement {
	var userPlacements []string
	for _, name := range table.Names() {
		if table.Placements[name].ScopeFor(goos) == harness.ScopeUser {
			userPlacements = append(userPlacements, name)
		}
	}

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
				location:  v.Location,
			})
		}
	}
	return entries
}

// UserPlanPath is where a run stores the plan listUserPlacements serves.
func UserPlanPath(appDataPath string) string {
	return filepath.Join(appDataPath, "user-plan.json")
}

// SaveUserPlan stores plan at UserPlanPath through a temporary file, so a
// concurrent listUserPlacements reads either the old plan or the new one.
func SaveUserPlan(appDataPath string, plan []UserPlacement) error {
	data, err := json.Marshal(listUserPlacementsResponse{Entries: plan})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(appDataPath, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(UserPlanPath(appDataPath), func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// LoadUserPlan reads the plan the last run stored; before the first run there
// is none, which is an empty plan. It reads no repository, so the helper gets
// its plan at logon even while the repository is unreachable.
func LoadUserPlan(appDataPath string) ([]UserPlacement, error) {
	data, err := os.ReadFile(UserPlanPath(appDataPath))
	if errors.Is(err, fs.ErrNotExist) {
		return []UserPlacement{}, nil
	}
	if err != nil {
		return nil, err
	}
	var stored listUserPlacementsResponse
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode %s: %w", UserPlanPath(appDataPath), err)
	}
	if stored.Entries == nil {
		stored.Entries = []UserPlacement{}
	}
	return stored.Entries, nil
}

// StageUserArtifacts makes dir hold exactly the archives plan references:
// each non-held entry's archive is downloaded and verified through zip (its
// cache and repository) and copied in when missing or different, and files
// no entry references are removed. A failure is logged and skips only its
// entry; the service never writes the user placements themselves.
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
	for _, e := range plan {
		if e.Held {
			continue
		}
		// Verify logs a warning for a missing file, which a first run has.
		if _, err := os.Stat(e.Archive); err == nil && download.Verify(e.Archive, e.Hash) {
			continue
		}
		t := installer.Target{Placement: e.Placement, Variant: catalog.Variant{Location: e.location, Hash: e.Hash, TreeHash: e.TreeHash}}
		cached, err := zip.Fetch(ctx, t)
		if err == nil {
			err = copyFileAtomic(cached, e.Archive)
		}
		if err != nil {
			slog.Warn("unable to stage user-placement archive", "item", e.Item, "placement", e.Placement, "err", err)
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
// info. Nothing is kept beyond the log.
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
	return writeFileAtomic(dst, func(w io.Writer) error {
		_, err := io.Copy(w, in)
		return err
	})
}

// writeFileAtomic writes dst through a temporary file beside it, renamed into
// place once complete.
func writeFileAtomic(dst string, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".staging-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	// Users read the staged archives; CreateTemp's 0600 would hide them off
	// Windows, where no inherited ACL applies.
	if err := tmp.Chmod(0o644); err != nil {
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
		return listUserPlacementsResponse{Entries: resp.UserPlacements}, nil
	}
	params, err := decodeParams[reportUserPlacementsParams](req.Params)
	if err != nil || params.Results == nil {
		return nil, newError(codeInvalidParams, req.Method+" requires params.results", "")
	}
	who, _ := requester(conn, logger)
	logUserPlacementResults(who, params.Results)
	return reportUserPlacementsResponse{}, nil
}

// sendUserPlacementCommand is `-S ListUserPlacements`, one JSON line per
// entry, and `-S ReportUserPlacements:item,placement,outcome[,error]`, which
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
	entries, err := client.ListUserPlacements(ctx)
	if err != nil {
		return CommandResponse{}, err
	}
	resp := CommandResponse{Status: "ok", UserPlacements: entries, Items: make([]string, 0, len(entries))}
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return CommandResponse{}, fmt.Errorf("failed to encode user placement %q: %w", e.Item, err)
		}
		resp.Items = append(resp.Items, string(line))
	}
	return resp, nil
}
