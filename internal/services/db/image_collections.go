// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

// ReserveImageCollection durably fences one exact set before any remote creation.
// An existing incomplete reservation has an unknown outcome and cannot be retried.
func (r *SQLiteRepository) ReserveImageCollection(
	ctx context.Context,
	binding api.PreparedMediaBinding,
	record api.ImageCollection,
) (*api.ImageCollection, bool, error) {
	bound, err := normalizePreparedMediaBinding(binding)
	if err != nil || record.Fingerprint == "" || record.ContentFingerprint == "" || record.Completed {
		return nil, false, errors.New("db: invalid image collection reservation")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, false, fmt.Errorf("db: encode image collection: %w", err)
	}
	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO image_collections (source_path, prepared_generation, fingerprint, content_fingerprint, completed, record_json) VALUES (?, ?, ?, ?, 0, ?) ON CONFLICT DO NOTHING`,
		bound.SourcePath,
		bound.PreparedGeneration,
		record.Fingerprint,
		record.ContentFingerprint,
		string(data),
	)
	if err != nil {
		return nil, false, fmt.Errorf("db: reserve image collection: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, false, fmt.Errorf("db: image collection reservation result: %w", err)
	}
	if count == 1 {
		return nil, true, nil
	}
	existing, err := r.LoadImageCollection(ctx, binding, record.Fingerprint)
	return existing, false, err
}

// LoadImageCollection returns the exact durable attempt, including unknown outcomes.
func (r *SQLiteRepository) LoadImageCollection(ctx context.Context, binding api.PreparedMediaBinding, fingerprint string) (*api.ImageCollection, error) {
	bound, err := normalizePreparedMediaBinding(binding)
	if err != nil {
		return nil, err
	}
	var data string
	err = r.db.QueryRowContext(
		ctx,
		`SELECT record_json FROM image_collections WHERE source_path = ? AND prepared_generation = ? AND fingerprint = ?`,
		bound.SourcePath,
		bound.PreparedGeneration,
		fingerprint,
	).Scan(
		&data,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("db: load image collection: %w", err)
	}
	var record api.ImageCollection
	if err := json.Unmarshal([]byte(data), &record); err != nil {
		return nil, fmt.Errorf("db: decode image collection: %w", err)
	}
	return &record, nil
}

// CompleteImageCollection publishes a set only after all members were uploaded.
func (r *SQLiteRepository) CompleteImageCollection(ctx context.Context, binding api.PreparedMediaBinding, record api.ImageCollection) error {
	bound, err := normalizePreparedMediaBinding(binding)
	if err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return fmt.Errorf("db: validate image collection: %w", err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("db: encode completed image collection: %w", err)
	}
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE image_collections SET completed = 1, record_json = ? WHERE source_path = ? AND prepared_generation = ? AND fingerprint = ? AND completed = 0`,
		string(data),
		bound.SourcePath,
		bound.PreparedGeneration,
		record.Fingerprint,
	)
	if err != nil {
		return fmt.Errorf("db: complete image collection: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("db: completed image collection result: %w", err)
	}
	if count != 1 {
		return errors.New("db: image collection reservation is unavailable")
	}
	return nil
}

// ReleaseImageCollection removes only a reservation known to have made no remote object.
func (r *SQLiteRepository) ReleaseImageCollection(ctx context.Context, binding api.PreparedMediaBinding, fingerprint string) error {
	bound, err := normalizePreparedMediaBinding(binding)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(
		ctx,
		`DELETE FROM image_collections WHERE source_path = ? AND prepared_generation = ? AND fingerprint = ? AND completed = 0`,
		bound.SourcePath,
		bound.PreparedGeneration,
		fingerprint,
	)
	if err != nil {
		return fmt.Errorf("db: release image collection reservation: %w", err)
	}
	return nil
}

func migrateImageCollections(ctx context.Context, exec migrationExecutor) error {
	_, err := exec.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS image_collections (
 source_path TEXT NOT NULL,
 prepared_generation INTEGER NOT NULL,
 fingerprint TEXT NOT NULL,
 content_fingerprint TEXT NOT NULL,
 completed INTEGER NOT NULL DEFAULT 0,
 record_json TEXT NOT NULL,
 PRIMARY KEY (source_path, prepared_generation, fingerprint)
 );
 CREATE UNIQUE INDEX IF NOT EXISTS idx_image_collection_pending ON image_collections(source_path, content_fingerprint) WHERE completed = 0`)
	if err != nil {
		return fmt.Errorf("db: migrate image collections: %w", err)
	}
	return nil
}
