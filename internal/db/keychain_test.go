package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeychainMigrationAppliesToExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gitmoot.db")
	legacy, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	version := 0
	for i, migration := range migrations {
		if strings.Contains(migration, "CREATE TABLE keychain_keys") {
			version = i + 1
			break
		}
	}
	if version == 0 {
		t.Fatal("keychain migration not found")
	}
	if _, err := legacy.db.Exec(`DROP TABLE keychain_grants; DROP TABLE keychain_keys; DELETE FROM schema_migrations WHERE version = ?`, version); err != nil {
		t.Fatalf("rewind keychain migration: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open existing database: %v", err)
	}
	defer store.Close()
	for _, table := range []string{"keychain_keys", "keychain_grants"} {
		if ok, err := store.HasTable(context.Background(), table); err != nil || !ok {
			t.Fatalf("table %s ok=%v err=%v", table, ok, err)
		}
	}
}

func TestKeychainCRUDGrantIdempotenceAndForcedCleanup(t *testing.T) {
	store := openPipelineStore(t)
	ctx := context.Background()
	if err := store.CreateOrUpdatePipeline(ctx, samplePipeline()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddKeychainKey(ctx, "BAD", "other"); err == nil {
		t.Fatal("invalid mode was accepted")
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO keychain_keys(name, mode) VALUES ('RAW_BAD', 'other')`); err == nil {
		t.Fatal("migration CHECK accepted an invalid mode")
	}
	injected, err := store.AddKeychainKey(ctx, "API_TOKEN", KeychainModeInjected)
	if err != nil || injected.Name != "API_TOKEN" || injected.Mode != KeychainModeInjected || injected.CreatedAt == "" {
		t.Fatalf("AddKeychainKey injected = %+v err=%v", injected, err)
	}
	if _, err := store.AddKeychainKey(ctx, "API_TOKEN", KeychainModeInjected); err == nil {
		t.Fatal("duplicate key name was accepted")
	}
	if _, err := store.AddKeychainKey(ctx, "MODEL_TOKEN", KeychainModeProxied); err != nil {
		t.Fatal(err)
	}
	if changed, err := store.GrantKeychainKey(ctx, KeychainConsumerPipeline, "deploy-flow", "MODEL_TOKEN"); changed || !errors.Is(err, ErrKeychainProxiedGrant) {
		t.Fatalf("proxied grant = changed %v err %v", changed, err)
	}
	changed, err := store.GrantKeychainKey(ctx, KeychainConsumerPipeline, "deploy-flow", "API_TOKEN")
	if err != nil || !changed {
		t.Fatalf("first grant = changed %v err %v", changed, err)
	}
	changed, err = store.GrantKeychainKey(ctx, KeychainConsumerPipeline, "deploy-flow", "API_TOKEN")
	if err != nil || changed {
		t.Fatalf("duplicate grant = changed %v err %v", changed, err)
	}
	granted, found, err := store.GetGrantedKey(ctx, KeychainConsumerPipeline, "deploy-flow", "API_TOKEN")
	if err != nil || !found || granted.Mode != KeychainModeInjected {
		t.Fatalf("GetGrantedKey = %+v found=%v err=%v", granted, found, err)
	}
	if removed, count, err := store.RemoveKeychainKey(ctx, "API_TOKEN", false); removed || count != 1 || !errors.Is(err, ErrKeychainKeyHasGrants) {
		t.Fatalf("guarded remove = removed %v count %d err %v", removed, count, err)
	}
	removed, count, err := store.RemoveKeychainKey(ctx, "API_TOKEN", true)
	if err != nil || !removed || count != 1 {
		t.Fatalf("forced remove = removed %v count %d err %v", removed, count, err)
	}
	if grants, err := store.ListKeychainGrants(ctx, "API_TOKEN"); err != nil || len(grants) != 0 {
		t.Fatalf("grants after force = %+v err=%v", grants, err)
	}
	if changed, err := store.RevokeKeychainKey(ctx, KeychainConsumerPipeline, "deploy-flow", "API_TOKEN"); err != nil || changed {
		t.Fatalf("idempotent revoke = changed %v err %v", changed, err)
	}
	keys, err := store.ListKeychainKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].Name != "MODEL_TOKEN" {
		t.Fatalf("keys = %+v err=%v", keys, err)
	}
}

func TestDeletePipelineCleansKeychainGrantsInSameTransaction(t *testing.T) {
	store := openPipelineStore(t)
	ctx := context.Background()
	if err := store.CreateOrUpdatePipeline(ctx, samplePipeline()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddKeychainKey(ctx, "API_TOKEN", KeychainModeInjected); err != nil {
		t.Fatal(err)
	}
	if changed, err := store.GrantKeychainKey(ctx, KeychainConsumerPipeline, "deploy-flow", "API_TOKEN"); err != nil || !changed {
		t.Fatalf("grant = changed %v err %v", changed, err)
	}
	if removed, err := store.DeletePipeline(ctx, "deploy-flow"); err != nil || !removed {
		t.Fatalf("DeletePipeline = removed %v err %v", removed, err)
	}
	if grants, err := store.ListKeychainGrants(ctx, "API_TOKEN"); err != nil || len(grants) != 0 {
		t.Fatalf("pipeline grants after delete = %+v err=%v", grants, err)
	}
	if _, found, err := store.GetKeychainKey(ctx, "API_TOKEN"); err != nil || !found {
		t.Fatalf("key metadata removed with pipeline: found=%v err=%v", found, err)
	}
}
