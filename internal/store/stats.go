package store

// StatBucket ist ein einzelner Balken in der Statistik-Ansicht (Label + Anzahl).
type StatBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// LibraryStatDetail ist die Antwort für die "Statistik"-Ansicht im Frontend:
// Gesamtzahlen + drei Verteilungen (Auflösung/Filetyp/Länge), skaliert auf
// eine Bibliothek oder — bei gesetztem folder — rekursiv auf einen Unterordner
// (Scope-Konvention identisch zu ListItems/CountItems: folder="" = ganze
// Library, sonst rel_path LIKE folder/%).
type LibraryStatDetail struct {
	TotalCount       int          `json:"totalCount"`
	TotalSizeBytes    int64        `json:"totalSizeBytes"`
	TotalDurationSec  float64      `json:"totalDurationSec"`
	ByResolution      []StatBucket `json:"byResolution"`
	ByContainer       []StatBucket `json:"byContainer"`
	ByDuration        []StatBucket `json:"byDuration"`
}

// GetLibraryStatDetail aggregiert Auflösung/Filetyp/Länge-Verteilungen rein
// per SQL (COUNT/SUM/CASE-WHEN) — es werden keine Item-Rows nach Go geladen,
// nur die fertigen Aggregat-Zahlen. Zwei indexierte Scans (items_library_idx
// bzw. items_lib_relpath_idx bei Folder-Scope) über die Bibliothek, keine
// zusätzliche Last für den normalen Grid-Betrieb.
func (s *Store) GetLibraryStatDetail(libraryID int64, folder string) (*LibraryStatDetail, error) {
	where := `WHERE library_id = ?`
	args := []any{libraryID}
	if folder != "" {
		where += ` AND rel_path LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(folder)+"/%")
	}

	res := &LibraryStatDetail{}

	// Effektive Höhe wie im Auflösungs-Filter: max(height, width*9/16), damit
	// Cinemascope-Filme in den richtigen Bucket fallen (siehe resLabel/ResBuckets).
	const effH = `MAX(height, (width * 9 / 16))`
	overviewQ := `
		SELECT
			COUNT(*),
			COALESCE(SUM(size_bytes), 0),
			COALESCE(SUM(duration_sec), 0),
			SUM(CASE WHEN ` + effH + ` >= 2000 THEN 1 ELSE 0 END),
			SUM(CASE WHEN ` + effH + ` BETWEEN 1400 AND 1999 THEN 1 ELSE 0 END),
			SUM(CASE WHEN ` + effH + ` BETWEEN 1000 AND 1399 THEN 1 ELSE 0 END),
			SUM(CASE WHEN ` + effH + ` BETWEEN 700 AND 999 THEN 1 ELSE 0 END),
			SUM(CASE WHEN ` + effH + ` BETWEEN 540 AND 699 THEN 1 ELSE 0 END),
			SUM(CASE WHEN ` + effH + ` BETWEEN 500 AND 539 THEN 1 ELSE 0 END),
			SUM(CASE WHEN ` + effH + ` BETWEEN 440 AND 499 THEN 1 ELSE 0 END),
			SUM(CASE WHEN ` + effH + ` < 440 THEN 1 ELSE 0 END),
			SUM(CASE WHEN duration_sec < 600 THEN 1 ELSE 0 END),
			SUM(CASE WHEN duration_sec >= 600 AND duration_sec < 1800 THEN 1 ELSE 0 END),
			SUM(CASE WHEN duration_sec >= 1800 AND duration_sec < 3600 THEN 1 ELSE 0 END),
			SUM(CASE WHEN duration_sec >= 3600 AND duration_sec < 7200 THEN 1 ELSE 0 END),
			SUM(CASE WHEN duration_sec >= 7200 THEN 1 ELSE 0 END)
		FROM items ` + where

	var c4k, c2k, c1080, c720, c576, c540, c480, cLow int
	var d10, d30, d60, d120, d120plus int
	err := s.db.QueryRow(overviewQ, args...).Scan(
		&res.TotalCount, &res.TotalSizeBytes, &res.TotalDurationSec,
		&c4k, &c2k, &c1080, &c720, &c576, &c540, &c480, &cLow,
		&d10, &d30, &d60, &d120, &d120plus,
	)
	if err != nil {
		return nil, err
	}

	addNonZero := func(dst *[]StatBucket, label string, count int) {
		if count > 0 {
			*dst = append(*dst, StatBucket{Label: label, Count: count})
		}
	}
	addNonZero(&res.ByResolution, "4K", c4k)
	addNonZero(&res.ByResolution, "2K", c2k)
	addNonZero(&res.ByResolution, "1080p", c1080)
	addNonZero(&res.ByResolution, "720p", c720)
	addNonZero(&res.ByResolution, "576p", c576)
	addNonZero(&res.ByResolution, "540p", c540)
	addNonZero(&res.ByResolution, "480p", c480)
	addNonZero(&res.ByResolution, "≤360p", cLow)

	addNonZero(&res.ByDuration, "< 10 Min", d10)
	addNonZero(&res.ByDuration, "10–30 Min", d30)
	addNonZero(&res.ByDuration, "30–60 Min", d60)
	addNonZero(&res.ByDuration, "1–2 Std", d120)
	addNonZero(&res.ByDuration, "> 2 Std", d120plus)

	// Filetyp/Container: offene Wertemenge, daher eigener GROUP BY-Query statt
	// CASE-WHEN-Liste. Gleicher WHERE-Scope, gleicher Index.
	containerQ := `SELECT COALESCE(NULLIF(container, ''), '(unbekannt)'), COUNT(*)
		FROM items ` + where + ` GROUP BY container ORDER BY COUNT(*) DESC`
	rows, err := s.db.Query(containerQ, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b StatBucket
		if err := rows.Scan(&b.Label, &b.Count); err != nil {
			return nil, err
		}
		res.ByContainer = append(res.ByContainer, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return res, nil
}
