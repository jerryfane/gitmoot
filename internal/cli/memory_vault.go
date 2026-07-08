package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/memory"
)

// vaultSchemaVersion is the on-disk manifest schema version. Bump only on a
// breaking change to the vault layout/manifest shape (P2's importer keys off it).
const vaultSchemaVersion = 1

// vaultLinkK is the number of [[co-occurrence links]] rendered per note.
const vaultLinkK = 5

// vaultManifest is the vault's staleness anchor, written as manifest.json at the
// vault root. snapshot_hash is memory.VaultSnapshotHash over the exported notes;
// P2's import regenerates a fresh export and aborts if the hashes disagree.
type vaultManifest struct {
	SchemaVersion int    `json:"schema_version"`
	SnapshotHash  string `json:"snapshot_hash"`
}

// vaultExportResult is the --json summary of an export run.
type vaultExportResult struct {
	Out           string   `json:"out"`
	SchemaVersion int      `json:"schema_version"`
	SnapshotHash  string   `json:"snapshot_hash"`
	Memories      int      `json:"memories"`
	Indexes       int      `json:"indexes"`
	Owners        []string `json:"owners"`
}

// runMemoryVault dispatches `gitmoot memory vault …`. P1 (#737) ships `export`
// only; import/ingest are P2/P3, behind this same command.
func runMemoryVault(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printMemoryVaultUsage(stdout)
		return 0
	}
	switch args[0] {
	case "export":
		return runMemoryVaultExport(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown memory vault command %q\n\n", args[0])
		printMemoryVaultUsage(stderr)
		return 2
	}
}

func printMemoryVaultUsage(w io.Writer) {
	fmt.Fprintln(w, "Render agent memory as a disposable Obsidian-compatible vault (#737).")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  gitmoot memory vault export [--out DIR] [--agent NAME] [--json]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  export  regenerate a deterministic vault (one note per confirmed memory,")
	fmt.Fprintln(w, "          per-owner index notes, and a manifest). The vault is a DERIVED VIEW:")
	fmt.Fprintln(w, "          the SQLite store stays the only source of truth, so the output is")
	fmt.Fprintln(w, "          disposable and byte-identical for an unchanged store. Default --out is")
	fmt.Fprintln(w, "          a vault/ directory under the home's evals area.")
}

// vaultNote is one rendered memory note held in memory before the atomic commit.
type vaultNote struct {
	filename  string
	bytes     []byte
	digest    memory.VaultFileDigest
	ownerKey  memory.VaultOwnerKey
	memRecord memory.VaultMemory
}

func runMemoryVaultExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory vault export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	out := fs.String("out", "", "output directory for the vault (default: <home>/evals/vault)")
	agent := fs.String("agent", "", "export only this agent owner's memories")
	jsonOut := fs.Bool("json", false, "print the export summary as JSON")
	if err := parseMemoryFlags(fs, args); err != nil {
		return memoryFlagExit(err)
	}

	outDir := *out
	if outDir == "" {
		paths, err := pathsFromFlag(*home)
		if err != nil {
			fmt.Fprintf(stderr, "memory vault export: %v\n", err)
			return 1
		}
		outDir = filepath.Join(paths.Evals, "vault")
	}
	outDir = filepath.Clean(outDir)

	var result vaultExportResult
	err := withReadOnlyStore(*home, func(store *db.Store) error {
		notes, indexes, snapshotHash, owners, err := buildVault(context.Background(), store, *agent)
		if err != nil {
			return err
		}
		if err := commitVault(outDir, notes, indexes, snapshotHash); err != nil {
			return err
		}
		result = vaultExportResult{
			Out:           outDir,
			SchemaVersion: vaultSchemaVersion,
			SnapshotHash:  snapshotHash,
			Memories:      len(notes),
			Indexes:       len(indexes),
			Owners:        owners,
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "memory vault export: %v\n", err)
		return 1
	}

	if *jsonOut {
		if err := writeJSON(stdout, result); err != nil {
			fmt.Fprintf(stderr, "memory vault export: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "exported %d memory note(s) and %d index note(s) to %s\n", result.Memories, result.Indexes, result.Out)
	fmt.Fprintf(stdout, "snapshot %s (schema v%d)\n", result.SnapshotHash, result.SchemaVersion)
	fmt.Fprintln(stdout, "(view, not replica: the vault is regenerated from the store on every export and is safe to delete)")
	return 0
}

// vaultIndexFile is a rendered per-owner index note.
type vaultIndexFile struct {
	filename string
	bytes    []byte
}

// buildVault reads the store (READ-ONLY) and renders every note deterministically
// in memory. It performs ZERO writes to any table. Returns the memory notes, the
// per-owner index notes, the manifest snapshot hash, and the sorted owner labels.
func buildVault(ctx context.Context, store *db.Store, agent string) ([]vaultNote, []vaultIndexFile, string, []string, error) {
	rows, err := store.ListConfirmedMemoriesForVault(ctx, agent)
	if err != nil {
		return nil, nil, "", nil, err
	}

	notes := make([]vaultNote, 0, len(rows))
	byOwner := make(map[memory.VaultOwnerKey][]memory.VaultMemory)
	var ownerOrder []memory.VaultOwnerKey

	for _, r := range rows {
		m := toVaultMemory(r)
		links, err := vaultLinksFor(ctx, store, r)
		if err != nil {
			return nil, nil, "", nil, err
		}
		body := []byte(memory.RenderVaultNote(m, links))
		sum := sha256.Sum256(body)
		note := vaultNote{
			filename:  memory.VaultFilename(m.ID, m.Key),
			bytes:     body,
			digest:    memory.VaultFileDigest{ID: m.ID, UpdatedAt: m.UpdatedAt, Sum: hex.EncodeToString(sum[:])},
			ownerKey:  m.OwnerKey(),
			memRecord: m,
		}
		notes = append(notes, note)
		if _, seen := byOwner[note.ownerKey]; !seen {
			ownerOrder = append(ownerOrder, note.ownerKey)
		}
		byOwner[note.ownerKey] = append(byOwner[note.ownerKey], m)
	}

	digests := make([]memory.VaultFileDigest, 0, len(notes))
	for _, n := range notes {
		digests = append(digests, n.digest)
	}
	snapshotHash := memory.VaultSnapshotHash(digests)

	// Owner index notes, one per owner, filenames deterministic.
	sort.Slice(ownerOrder, func(i, j int) bool { return vaultOwnerLess(ownerOrder[i], ownerOrder[j]) })
	indexes := make([]vaultIndexFile, 0, len(ownerOrder))
	owners := make([]string, 0, len(ownerOrder))
	for _, ok := range ownerOrder {
		indexes = append(indexes, vaultIndexFile{
			filename: memory.VaultIndexFilename(ok),
			bytes:    []byte(memory.RenderVaultIndex(ok, byOwner[ok])),
		})
		owners = append(owners, vaultOwnerLabel(ok))
	}

	return notes, indexes, snapshotHash, owners, nil
}

// vaultLinksFor computes the deterministic top-K co-occurrence links for one
// memory: sanitize key+content into a MATCH query, run the vault-local bm25→id
// helper, drop self, and cap at K. Returns links already carrying their target
// filename stems; RenderVaultNote sorts them by target id.
func vaultLinksFor(ctx context.Context, store *db.Store, src db.ConfirmedMemory) ([]memory.VaultLink, error) {
	matchQuery := memory.SanitizeFTSQuery(src.Key + " " + src.Content)
	if matchQuery == "" {
		return nil, nil
	}
	// Fetch one extra so excluding self still leaves up to K candidates.
	related, err := store.QueryConfirmedMemoryVaultLinks(ctx, src.Owner, src.Repo, matchQuery, vaultLinkK+1)
	if err != nil {
		return nil, err
	}
	links := make([]memory.VaultLink, 0, vaultLinkK)
	for _, rel := range related {
		if rel.ID == src.ID {
			continue
		}
		links = append(links, memory.VaultLink{
			TargetID: rel.ID,
			Stem:     memory.VaultStem(rel.ID, rel.Key),
			Key:      rel.Key,
		})
		if len(links) >= vaultLinkK {
			break
		}
	}
	return links, nil
}

// commitVault writes the whole tree to a sibling temp dir then atomically renames
// it over --out, so a reader never sees a half-written vault. The store is never
// touched; only the output directory changes.
func commitVault(outDir string, notes []vaultNote, indexes []vaultIndexFile, snapshotHash string) error {
	parent := filepath.Dir(outDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create vault parent: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".gitmoot-vault-tmp-*")
	if err != nil {
		return fmt.Errorf("create vault temp dir: %w", err)
	}
	// Best-effort cleanup if we bail before the rename.
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmp)
		}
	}()

	for _, n := range notes {
		if err := os.WriteFile(filepath.Join(tmp, n.filename), n.bytes, 0o644); err != nil {
			return fmt.Errorf("write note %s: %w", n.filename, err)
		}
	}
	for _, idx := range indexes {
		if err := os.WriteFile(filepath.Join(tmp, idx.filename), idx.bytes, 0o644); err != nil {
			return fmt.Errorf("write index %s: %w", idx.filename, err)
		}
	}
	manifestBytes, err := json.MarshalIndent(vaultManifest{SchemaVersion: vaultSchemaVersion, SnapshotHash: snapshotHash}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(filepath.Join(tmp, "manifest.json"), manifestBytes, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	if err := os.RemoveAll(outDir); err != nil {
		return fmt.Errorf("clear existing vault: %w", err)
	}
	if err := os.Rename(tmp, outDir); err != nil {
		return fmt.Errorf("commit vault: %w", err)
	}
	committed = true
	return nil
}

func toVaultMemory(c db.ConfirmedMemory) memory.VaultMemory {
	return memory.VaultMemory{
		ID:           c.ID,
		OwnerKind:    c.Owner.Kind,
		OwnerRef:     c.Owner.Ref,
		OwnerVersion: c.Owner.Version,
		Repo:         c.Repo,
		Scope:        c.Scope,
		Key:          c.Key,
		Content:      c.Content,
		Provenance:   c.Provenance,
		SourceJob:    c.SourceJob,
		CreatedAt:    c.FirstConfirmedAt,
		UpdatedAt:    c.UpdatedAt,
		SupersededBy: c.SupersededBy,
	}
}

func vaultOwnerLess(a, b memory.VaultOwnerKey) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Ref != b.Ref {
		return a.Ref < b.Ref
	}
	return a.Version < b.Version
}

func vaultOwnerLabel(o memory.VaultOwnerKey) string {
	label := o.Kind + ":" + o.Ref
	if o.Version != "" {
		label += "@" + o.Version
	}
	return label
}
