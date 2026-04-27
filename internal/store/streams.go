package store

import (
	"database/sql"

	"github.com/boernie77/goldfish/internal/model"
)

// ReplaceItemStreams ersetzt die komplette Stream-Liste eines Items.
func (s *Store) ReplaceItemStreams(itemID int64, streams []model.ItemStream) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM item_streams WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	for _, st := range streams {
		isDef := 0
		if st.IsDefault {
			isDef = 1
		}
		isFor := 0
		if st.IsForced {
			isFor = 1
		}
		if _, err := tx.Exec(`
			INSERT INTO item_streams(item_id, stream_index, type, codec, language, title, channels, is_default, is_forced, field_order)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, itemID, st.Index, st.Type, st.Codec, st.Language, st.Title, st.Channels, isDef, isFor, st.FieldOrder); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ItemStreams liefert alle Streams eines Items, sortiert nach ffprobe-Index.
func (s *Store) ItemStreams(itemID int64) ([]model.ItemStream, error) {
	rows, err := s.db.Query(`
		SELECT stream_index, type, COALESCE(codec,''), COALESCE(language,''), COALESCE(title,''),
		       COALESCE(channels, 0), is_default, is_forced, COALESCE(field_order,'')
		FROM item_streams WHERE item_id = ? ORDER BY stream_index
	`, itemID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.ItemStream
	for rows.Next() {
		var st model.ItemStream
		var isDef, isFor int
		var channels sql.NullInt64
		if err := rows.Scan(&st.Index, &st.Type, &st.Codec, &st.Language, &st.Title,
			&channels, &isDef, &isFor, &st.FieldOrder); err != nil {
			return nil, err
		}
		if channels.Valid {
			st.Channels = int(channels.Int64)
		}
		st.IsDefault = isDef == 1
		st.IsForced = isFor == 1
		out = append(out, st)
	}
	return out, rows.Err()
}
