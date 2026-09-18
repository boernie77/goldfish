package store

// next_episode.go — Ermittlung der auf ein Serien-Item folgenden Episode.
//
// Einziger Zweck: dem Client sagen, was nach dem Ende der laufenden Folge
// automatisch starten würde ("Nächste Folge automatisch starten", User-Wunsch
// 2026-09-18). Bewusst SERVERSEITIG und nicht pro Client nachgebaut — sonst
// müsste jede der sieben Apps (Browser, iOS, macOS, tvOS, Android, Fire TV,
// Linux) dieselbe Reihenfolge- und Doppelfolgen-Logik ein zweites Mal
// korrekt implementieren.

// EpisodeCandidate — eine Kandidaten-Folge der laufenden Serie.
type EpisodeCandidate struct {
	ID         int64
	LibraryID  int64
	MetadataID int64
	Season     int
	Episode    int
}

// NextEpisodeCandidates liefert die nach diesem Item folgenden Episoden
// derselben Serie in Abspielreihenfolge (season, episode aufsteigend),
// maximal limit Stück.
//
// Regeln:
//   - Serienzugehörigkeit ausschließlich über metadata.parent_id. Das
//     gemeinsame Serien-Metadaten-Objekt ist der einzige Anker, der auch
//     Episoden aus getrennten physischen Ordnern zusammenbringt (Auto-Merge
//     doppelter Serien-Ordner) — der Ordnername ist dafür NICHT geeignet.
//   - Doppelfolgen zählen als Block: endet die aktuelle Datei erst bei
//     items.episode_end (S07E23E24 → 23/24), muss die nächste Folge hinter
//     diesem Ende liegen, nicht hinter der Startnummer.
//   - Mehrere Auflösungsvarianten derselben Folge sind mehrere items-Zeilen.
//     Pro (Staffel, Folge) wird genau eine Zeile zurückgegeben, und zwar die
//     mit der höheren Quelle (items.height) — dieselbe Wahl, die die
//     Kachelansicht als Vertreter trifft (groupVariants).
//   - Gibt es keine spätere Folge, ist die Liste leer (kein Fehler).
//
// ⚠ ACL und Altersfreigabe werden hier bewusst NICHT geprüft: der Store kennt
// keine Nutzerrechte, und ein Raten an dieser Stelle wäre genau das Leck, das
// die Datentrennung brechen würde. Deshalb liefert die Funktion MEHRERE
// Kandidaten — der Aufrufer (API-Schicht) geht sie der Reihe nach durch und
// nimmt den ersten, den der anfragende Nutzer überhaupt sehen darf
// (UserHasLibraryAccess + isAgeAllowedForUser). Ein Kandidat aus einer
// Bibliothek ohne Zugriff wird dabei schlicht übersprungen, nicht der
// Endpoint abgebrochen: der Nutzer hat Anspruch auf die nächste Folge, die
// ER sehen kann, nicht auf eine Fehlermeldung.
func (s *Store) NextEpisodeCandidates(itemID int64, limit int) ([]EpisodeCandidate, error) {
	if limit <= 0 {
		limit = 10
	}
	q := `
		WITH cur AS (
			SELECT COALESCE(m.parent_id, 0) AS show_id,
			       COALESCE(m.season, 0)     AS season,
			       CASE WHEN COALESCE(i.episode_end, 0) > 0 THEN i.episode_end
			            ELSE COALESCE(m.episode, 0) END AS ep_end
			FROM items i
			JOIN metadata m ON m.id = i.metadata_id
			WHERE i.id = ?
		),
		cand AS (
			SELECT i.id AS id, i.library_id AS library_id,
			       COALESCE(i.metadata_id, 0) AS metadata_id,
			       COALESCE(m.season, 0) AS season,
			       COALESCE(m.episode, 0) AS episode,
			       ROW_NUMBER() OVER (
			           PARTITION BY COALESCE(m.season, 0), COALESCE(m.episode, 0)
			           ORDER BY COALESCE(i.height, 0) DESC, i.id ASC
			       ) AS rn
			FROM items i
			JOIN metadata m ON m.id = i.metadata_id
			JOIN cur c ON c.show_id = m.parent_id
			WHERE c.show_id > 0
			  AND m.tmdb_type = 'episode'
			  AND (m.season > c.season
			       OR (m.season = c.season AND COALESCE(m.episode, 0) > c.ep_end))
		)
		SELECT id, library_id, metadata_id, season, episode
		FROM cand
		WHERE rn = 1
		ORDER BY season ASC, episode ASC
		LIMIT ?`
	rows, err := s.db.Query(q, itemID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []EpisodeCandidate{}
	for rows.Next() {
		var c EpisodeCandidate
		if err := rows.Scan(&c.ID, &c.LibraryID, &c.MetadataID, &c.Season, &c.Episode); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
