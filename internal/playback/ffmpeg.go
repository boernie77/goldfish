package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Profile definiert Auflösungs- und Bitraten-Zielwerte für einen Transcode.
type Profile struct {
	ID        string
	Label     string
	MaxHeight int // 0 = Original
	VideoKbps int // 0 = Qualitäts-basiert (QP 23)
	AudioKbps int
}

var Profiles = []Profile{
	{ID: "orig", Label: "Original (Qualität)", MaxHeight: 0, VideoKbps: 0, AudioKbps: 192},
	// 1080p — drei Bitratenstufen für unterschiedliche Bandbreiten.
	{ID: "1080p-hq", Label: "1080p · 8 Mbps (hoch)", MaxHeight: 1080, VideoKbps: 8000, AudioKbps: 192},
	{ID: "1080p", Label: "1080p · 5 Mbps (mittel)", MaxHeight: 1080, VideoKbps: 5000, AudioKbps: 160},
	{ID: "1080p-lq", Label: "1080p · 3 Mbps (niedrig)", MaxHeight: 1080, VideoKbps: 3000, AudioKbps: 128},
	// 720p
	{ID: "720p-hq", Label: "720p · 4 Mbps (hoch)", MaxHeight: 720, VideoKbps: 4000, AudioKbps: 160},
	{ID: "720p", Label: "720p · 2,5 Mbps (mittel)", MaxHeight: 720, VideoKbps: 2500, AudioKbps: 128},
	{ID: "720p-lq", Label: "720p · 1,5 Mbps (niedrig)", MaxHeight: 720, VideoKbps: 1500, AudioKbps: 96},
	// 480p
	{ID: "480p-hq", Label: "480p · 2 Mbps (hoch)", MaxHeight: 480, VideoKbps: 2000, AudioKbps: 128},
	{ID: "480p", Label: "480p · 1 Mbps (mittel)", MaxHeight: 480, VideoKbps: 1000, AudioKbps: 96},
	{ID: "480p-lq", Label: "480p · 600 kbps (niedrig)", MaxHeight: 480, VideoKbps: 600, AudioKbps: 96},
}

func ProfileByID(id string) Profile {
	for _, p := range Profiles {
		if p.ID == id {
			return p
		}
	}
	return Profiles[0]
}

// Session manages one ffmpeg transcode that produces an HLS playlist.
//
// fallbackStage beschreibt, wie eine Sitzung nach einem gescheiterten
// Hardware-Decode weiterläuft. Der Weg ist EINE Stufe pro Versuch — sonst
// entstünde bei einer wirklich kaputten Datei eine Endlosschleife.
type fallbackStage int

const (
	// stageHardware: Normalweg seit 2026-09-14 — VAAPI dekodiert UND encodiert,
	// die Bilder bleiben durchgehend Flächen der Grafikeinheit.
	stageHardware fallbackStage = iota
	// stageCPUEncodeVAAPI: CPU dekodiert, die Grafikeinheit encodiert
	// (`format=nv12,hwupload` + h264_vaapi). Gedacht für Dateien, an deren
	// DEKODER die Hardware scheitert, während der Encoder sie problemlos
	// kann — gemessen am 2026-09-22 mit AV1 (YouTube): 38,6 s CPU-Zeit im
	// reinen Software-Weg gegen 11,0 s auf diesem Weg für dieselben 20 s
	// Material, also Faktor 3,5.
	stageCPUEncodeVAAPI
	// stageFullSoftware: CPU dekodiert UND encodiert (libx264) — letzte Stufe,
	// keine weitere Wiederholung.
	stageFullSoftware
)

// sessionSpec haelt alles, was zum Starten einer Sitzung noetig ist. Getrennt
// gespeichert, damit dieselbe Sitzung per `RetryWithFallback` mit
// identischen Werten neu aufgesetzt werden kann.
type sessionSpec struct {
	itemID      int64
	inputPath   string
	profile     Profile
	audioIdx    int
	startSec    float64
	deinterlace bool
	audioOnly   bool
	// srcHeight: Hoehe der Originaldatei. Geht in die Kostenberechnung ein
	// (siehe transcodeCost) — das Dekodieren der Quelle faellt unabhaengig
	// von der Zielaufloesung an, eine 4K-Quelle kostet also auch dann viel,
	// wenn klein ausgegeben wird. 0 = unbekannt (wird als 4K behandelt).
	srcHeight int
	// vod: vorab berechnete Segmentierung (siehe vod.go), nil = EVENT-Playlist.
	// vodStartSeg: erstes Segment DIESES ffmpeg-Laufs (0 beim Sitzungsstart,
	// >0 nach einem Neustart bei einem Sprung).
	vod         *VODPlan
	vodStartSeg int
}

type Session struct {
	ID        string
	ItemID    int64
	Profile   string
	AudioIdx  int // -1 = default (erster Audio-Stream)
	Dir       string
	StartSec  float64
	StartedAt time.Time // Wall-Clock-Zeit beim Session-Erzeugen — fresh=1-Idempotenz
	Cmd       *exec.Cmd
	cancel    context.CancelFunc
	lastUsed  time.Time
	mu        sync.Mutex
	done      chan struct{}

	spec sessionSpec
	// stage: auf welchem Weg diese Sitzung gerade läuft. stageHardware ist der
	// Normalfall, die beiden anderen sind die Rückfallstufen nach einem
	// gescheiterten Hardware-Decode. Verhindert eine Endlos-Wiederholung —
	// jede Stufe wird höchstens einmal versucht.
	stage fallbackStage
	// failed: true NUR wenn ffmpeg mit einem echten Fehler endete (Exit-Code
	// != 0, NICHT durch unseren eigenen Stop()/Context-Abbruch). Gesetzt VOR
	// dem close(s.done), also fuer jeden Leser von Done()==true bereits
	// sichtbar. Unterscheidet den Fall, fuer den der 2026-09-17-Fix gedacht
	// war (WMV-Datei bricht nach 2s MIT FEHLER ab, muss sofort raus, sonst
	// blockiert sie das Budget) von einem ganz normal ERFOLGREICH beendeten
	// Transcode (Dateiende erreicht, komplette Playlist geschrieben) — DER
	// darf nicht sofort geloescht werden, der Client hat die letzten
	// Segmente evtl. noch nicht abgeholt. Bug gefunden 2026-09-17 (User-
	// Report "Source error" bei AV1-Dateien): der CPU-Fallback lief bei AV1
	// komplett durch (kein Fehler!), wurde aber vom GC trotzdem sofort samt
	// Cache-Verzeichnis geloescht, sobald Done() true war — Client bekam
	// 404 auf gerade geloeschte Segmente.
	failed bool
	// Playlist-Pacing (siehe pacing.go): wie viele Segmente die Playlist
	// zuletzt zeigte und wann sich das zuletzt geändert hat. Geschützt durch mu.
	paceExposed int
	paceChanged time.Time
}

// UsesSoftwareDecode meldet, ob diese Sitzung bereits vollständig per CPU
// läuft (letzte Rückfallstufe). Der API-Handler entscheidet damit, ob sich
// ein Rückfall überhaupt noch lohnt.
func (s *Session) UsesSoftwareDecode() bool { return s.stage == stageFullSoftware }

// FallbackStage meldet die aktuelle Stufe dieser Sitzung.
func (s *Session) FallbackStage() fallbackStage { return s.stage }

// CanFallback meldet, ob für diese Sitzung noch eine Rückfallstufe übrig ist.
func (s *Session) CanFallback() bool { return s.stage != stageFullSoftware }

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	cacheDir string
	hw       HWAccel
	// freshTokens: pro Session-Key der zuletzt akzeptierte `_t`-Token aus
	// der Player-URL. VHS laedt EVENT-Playlists periodisch mit DERSELBEN
	// URL (inkl. fresh=1 und _t=<page-load-time>). Solange der Token
	// gleich bleibt, erkennen wir einen VHS-Reload und tun NICHTS — die
	// laufende Session bleibt am Leben. Bei einem neuen Player-Open
	// generiert das Frontend `_t=Date.now()` neu → Token-Mismatch →
	// alte Session wird gekillt und frisch erzeugt.
	//
	// Die fruehere Wallclock-60-s-Variante hatte den Bug: nach 60 s
	// lief das Fenster ab und die naechste VHS-Reload killte die laufende
	// Session, Wiedergabe stallte zyklisch.
	freshTokens map[string]string
	// stoppedAt: Zeitpunkt des letzten expliziten Client-Stops (StopAllForItem)
	// pro Item. Siehe stopSuppressWindow unten in StartOrGet.
	stoppedAt map[int64]time.Time
	// vodMu/vodRestarts: serialisiert VOD-Neustarts und merkt sich pro
	// Sitzung den letzten (siehe EnsureVODSegment).
	vodMu       sync.Mutex
	vodRestarts map[string]time.Time
	// maxSessions: harte Obergrenze gleichzeitiger VIDEO-Transcodes. 0 =
	// unbegrenzt (nur fuer Tests; im Betrieb setzt main.go immer einen Wert).
	// Siehe ErrTooManySessions und den Limit-Block in StartOrGet.
	maxSessions int
}

// ErrTooManySessions meldet, dass das konfigurierte Limit gleichzeitiger
// Transcodes erreicht ist. Der API-Layer uebersetzt das in HTTP 503 mit einer
// verstaendlichen Meldung — bewusst KEIN 500, damit Clients es als temporaer
// erkennen und ein Retry sinnvoll ist.
var ErrTooManySessions = errors.New("zu viele gleichzeitige Transcodes")

// ErrStoppedRecently meldet, dass für dieses Item GERADE ein Client-Stop
// gemeldet wurde (siehe stopSuppressWindow in StartOrGet) und innerhalb des
// Sperrfensters keine neue Sitzung erzeugt wird.
//
// Warum ein eigener Sentinel: das ist ein TEMPORÄRER Zustand, kein
// Serverfehler. Der API-Layer macht daraus 503 + Retry-After statt 500 —
// AVPlayer und ExoPlayer laden am Ende einer EVENT-Playlist von sich aus
// erneut die Playlist nach, treffen dabei ins Sperrfenster und zeigten dem
// Nutzer sonst einen modalen Abspielfehler („Stream-Fehler (-16847) … HTTP
// 500", User-Report macOS 2026-09-18, direkt nach dem Folgen-Ende und
// gleichzeitig mit dem „Nächste Folge"-Hinweis). Mit 503 + Retry-After
// wiederholen die Player den Abruf still statt abzubrechen.
var ErrStoppedRecently = errors.New("wiedergabe wurde gerade beendet")

// ─── Gewichtetes Transcode-Budget ──────────────────────────────────────────
//
// Eine Sitzung zaehlt NICHT pauschal als "eine", sondern mit Kostenpunkten:
// eine 4K-Umwandlung belastet die Grafikeinheit um ein Vielfaches einer
// kleinen. Wuerde stumpf die Anzahl gezaehlt, blockierte ein 480p-Stream
// denselben Platz wie ein 4K-Stream und im Alltag bliebe Kapazitaet ungenutzt.
//
// Die Werte stammen aus einer Messung auf echter Hardware (Intel UHD 770,
// VAAPI, 60 s Material je Lauf, zweifach wiederholt, Werte stabil):
//
//	Quelle  → Ziel     Dauer    relativ
//	4K HEVC → 2160p    15,0 s   1,00
//	4K HEVC → 1080p     6,6 s   0,44
//	4K HEVC →  720p     5,6 s   0,37
//	1080p   → 1080p     5,8 s   0,39
//	1080p   →  720p     2,7 s   0,18
//	1080p   →  480p     1,8 s   0,12
//
// Zwei Dinge fallen daran auf und sind der Grund fuer die Formel unten:
//  1. **Die QUELLE zaehlt mit, nicht nur das Ziel.** Dasselbe Ziel (720p)
//     kostet aus einer 4K-Quelle 5,6 s, aus einer 1080p-Quelle nur 2,7 s —
//     das Dekodieren faellt unabhaengig vom Ziel an. Eine reine
//     Ziel-Gewichtung waere darum falsch.
//  2. **Unterhalb von 1080p flacht es ab.** Zwischen 720p und 480p liegt
//     wenig, weil dann der Decode dominiert, nicht der Encode.
//
// Die Punktwerte sind bewusst nach OBEN gerundet (jeder Wert liegt ueber dem
// gemessenen Anteil): eine Ueberschaetzung lehnt hoechstens eine Wiedergabe
// zu frueh ab, eine Unterschaetzung riskiert genau den Absturz, den das
// Limit verhindern soll.
const (
	// CostFullBudgetUnit: Kosten einer 4K→4K-Umwandlung, der teuerste Fall.
	// Alle anderen Werte sind Bruchteile davon. 100 statt 1, damit ohne
	// Fliesskomma gerechnet werden kann.
	CostFullBudgetUnit = 100
)

// transcodeCost liefert die Kostenpunkte einer Sitzung aus Quell- und
// Zielhoehe. `srcHeight` = Hoehe der Originaldatei (0 = unbekannt → wird
// vorsichtshalber als 4K behandelt), `targetHeight` = Profil-MaxHeight
// (0 = "Original", also so gross wie die Quelle).
func transcodeCost(srcHeight, targetHeight int) int {
	// Unbekannte Quelle: vom teuersten Fall ausgehen. Lieber eine Wiedergabe
	// zu frueh ablehnen als den Server ueberbuchen.
	if srcHeight <= 0 {
		srcHeight = 2160
	}
	// Profil "Original" (MaxHeight 0) bedeutet: Zielhoehe = Quellhoehe.
	if targetHeight <= 0 || targetHeight > srcHeight {
		targetHeight = srcHeight
	}
	switch {
	case srcHeight >= 1800: // 4K-Quelle
		switch {
		case targetHeight >= 1800:
			return 100 // gemessen 1,00
		case targetHeight >= 1000:
			return 50 // gemessen 0,44
		default:
			return 40 // gemessen 0,37
		}
	case srcHeight >= 1000: // 1080p/1440p-Quelle
		switch {
		case targetHeight >= 1000:
			return 50 // gemessen 0,39
		case targetHeight >= 600:
			return 25 // gemessen 0,18
		default:
			return 20 // gemessen 0,12
		}
	default: // 720p-Quelle oder kleiner — durchweg guenstig
		if targetHeight >= 600 {
			return 20
		}
		return 15
	}
}

// TranscodeCost ist der exportierte Zugang zu transcodeCost (fuer Tests und
// die Anzeige im Einstellungsdialog).
func TranscodeCost(srcHeight, targetHeight int) int { return transcodeCost(srcHeight, targetHeight) }

// maxSessionsHardCap: absolute Obergrenze der ANZAHL Sitzungen, unabhaengig
// vom Kostenbudget. Das Budget allein wuerde bei lauter sehr guenstigen
// Sitzungen (480p aus kleiner Quelle, 15 Punkte) rechnerisch ueber zwanzig
// gleichzeitige ffmpeg-Prozesse erlauben — die belasten zwar die
// Grafikeinheit kaum, kosten aber je Prozess Arbeitsspeicher, Dateihandles
// und Schreiblast im Cache-Verzeichnis. Dieser Deckel begrenzt das auf das
// Dreifache des eingestellten Werts.
const maxSessionsHardCapFactor = 3

// DefaultMaxSessions ist die Voreinstellung fuer gleichzeitige Video-Transcodes.
//
// 🔴 Hintergrund (User-Test 2026-09-16): beim Ausloten, wie viele 4K-Transcodes
// die iGPU schafft, liefen VIER gleichzeitig stabil — ACHT rissen den GESAMTEN
// Unraid-Host mit (nicht nur den Container: kompletter Reboot noetig). Vorher
// gab es ueberhaupt kein Limit; StartOrGet startete bedingungslos fuer jede
// Anfrage einen weiteren ffmpeg-Prozess. Vier ist deshalb die belegte
// Stabilitaetsgrenze dieser Hardware und damit der Default.
//
// Seit der Umstellung auf das gewichtete Budget bedeutet "4": vier
// gleichzeitige 4K→4K-Umwandlungen — ODER entsprechend mehr kleinere,
// z. B. rund acht 1080p- oder sechzehn 480p-Streams.
const DefaultMaxSessions = 4

func NewManager(cacheDir string, hw HWAccel) *Manager {
	_ = os.MkdirAll(cacheDir, 0o755)
	cleanStaleSessionDirs(cacheDir)
	m := &Manager{
		sessions:    map[string]*Session{},
		cacheDir:    cacheDir,
		hw:          hw,
		freshTokens: map[string]string{},
		stoppedAt:   map[int64]time.Time{},
		vodRestarts: map[string]time.Time{},
		maxSessions: DefaultMaxSessions,
	}
	go m.gcLoop()
	return m
}

// SetHWAccel wechselt das aktive Backend zur Laufzeit (z. B. wenn der User in
// den Settings von VAAPI auf NVENC wechselt). Bereits laufende Sessions
// behalten ihre alte Konfiguration; neue Sessions nutzen das neue Backend.
func (m *Manager) SetHWAccel(hw HWAccel) {
	m.mu.Lock()
	m.hw = hw
	m.mu.Unlock()
}

// SetMaxSessions setzt die Obergrenze gleichzeitiger Video-Transcodes zur
// Laufzeit (Zahnrad-Menue → Einstellungen). Bereits laufende Sessions werden
// NICHT gekillt, wenn der Wert gesenkt wird — das Limit wirkt erst auf die
// naechste Neu-Anfrage, damit niemandem mitten im Film das Bild abreisst.
func (m *Manager) SetMaxSessions(n int) {
	if n < 0 {
		n = 0
	}
	m.mu.Lock()
	m.maxSessions = n
	m.mu.Unlock()
}

// activeVideoSessionsLocked zaehlt laufende Transcodes, die die Grafikeinheit
// belasten. Reine Audio-Sessions (Musikwiedergabe) sind ausgenommen: sie
// kodieren nur eine Tonspur, kosten weder GPU-Speicher noch nennenswert CPU
// und duerfen deshalb nicht dazu fuehren, dass ein Film abgelehnt wird.
//
// 🔴 Gezaehlt wird nur, wessen ffmpeg noch LAEUFT (seit 2026-09-25). Eine
// erfolgreich fertig umgewandelte Sitzung bleibt bewusst bis zum Leerlauf-GC
// im Pool (der Client holt die letzten Segmente evtl. noch ab, siehe
// `failed`), belastet die Grafikeinheit aber nicht mehr. Vorher belegte sie
// trotzdem 30 Minuten lang ihre Budgetpunkte: beim schnellen Durchklicken
// kurzer Clips (per VAAPI in Sekunden fertig) war das Budget live mit
// „355 von 400 Punkten, 8 Sitzungen" voll, waehrend `docker top` keinen
// einzigen ffmpeg-Prozess zeigte — jede neue Wiedergabe wurde abgelehnt.
//
// Der Aufrufer MUSS m.mu halten.
func (m *Manager) activeVideoSessionsLocked() int {
	n := 0
	for _, s := range m.sessions {
		if !s.spec.audioOnly && !s.Done() {
			n++
		}
	}
	return n
}

// activeCostLocked summiert die Kostenpunkte aller laufenden Video-Sitzungen
// (siehe transcodeCost). Beendete Sitzungen zaehlen nicht, siehe
// activeVideoSessionsLocked. Der Aufrufer MUSS m.mu halten.
func (m *Manager) activeCostLocked() int {
	sum := 0
	for _, s := range m.sessions {
		if s.spec.audioOnly || s.Done() {
			continue
		}
		sum += transcodeCost(s.spec.srcHeight, s.spec.profile.MaxHeight)
	}
	return sum
}

// ActiveVideoSessions liefert die Zahl laufender Video-Transcodes (fuer
// /api/health und die Auslastungsanzeige).
func (m *Manager) ActiveVideoSessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeVideoSessionsLocked()
}

// ActiveLoadPercent liefert die aktuelle Auslastung in Prozent des Budgets
// (100 = voll). Fuer die Anzeige im Einstellungsdialog.
func (m *Manager) ActiveLoadPercent() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maxSessions <= 0 {
		return 0
	}
	return m.activeCostLocked() * 100 / (m.maxSessions * CostFullBudgetUnit)
}

// MaxSessions liefert die aktuell konfigurierte Obergrenze (0 = unbegrenzt).
func (m *Manager) MaxSessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxSessions
}

// sessionIdleTimeout: wie lange eine Transcode-Session ohne jeden Touch()
// (Segment-Fetch, Playlist-Reload oder /progress-Poll) am Leben bleibt, bevor
// der GC sie killt. War historisch 5 Minuten (siehe [[project_fix_pause_resume_gc]]
// im Repo-Gedächtnis) — reichte für den Browser (VHS pollt /progress
// unbedingt auch während Pause weiter), brach aber auf dem Apple-TV-Client
// nach einer nur ~5-minütigen Pause ab (User-Report 2026-09-08): AVPlayer
// pollt die EVENT-Playlist bei einer echten Nutzer-Pause offenbar NICHT
// weiter (im Gegensatz zu VHS im Browser) — ohne jeden Touch() während der
// Pause killte der GC die ffmpeg-Session nach 5 Min, der Client spielte
// danach nur noch seinen lokalen Restpuffer (~60s, passend zur konfigurierten
// Bufferlänge) und brach dann mit 404 auf ein nicht mehr existierendes
// Segment ab. Auf 30 Minuten angehoben — deckt realistische Pausen (Anruf,
// Tür, Pipi-Pause) clientunabhängig ab, ohne auf ein bestimmtes Polling-
// Verhalten einzelner Clients angewiesen zu sein. Verwaiste Sessions kosten
// nur Cache-Platz (unter /config/cache/{sessionID}/) bis zum nächsten
// GC-Lauf, kein laufendes ffmpeg mehr nach Ablauf.
const sessionIdleTimeout = 30 * time.Minute

func (m *Manager) gcLoop() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		m.mu.Lock()
		for id, s := range m.sessions {
			s.mu.Lock()
			idle := time.Since(s.lastUsed)
			s.mu.Unlock()
			// 🔴 Tote Sitzungen SOFORT ausbuchen (seit 2026-09-17) — ABER
			// NUR wenn sie mit einem echten Fehler endeten (Failed()).
			//
			// Vorher zaehlte allein der Leerlauf: eine Sitzung, deren ffmpeg
			// nach zwei Sekunden an der Datei gescheitert war, blieb volle
			// 30 Minuten im Pool stehen. Sie verbrauchte zwar keine Rechen-
			// zeit mehr, belegte aber weiter ihren Platz im Transcode-Budget
			// und in der Sitzungs-Obergrenze. Am 2026-09-17 live beobachtet:
			// mehrere an einer WMV-Datei gescheiterte Versuche summierten
			// sich, bis eine voellig gesunde Wiedergabe mit
			// „Sitzungs-Obergrenze erreicht (12)" abgelehnt wurde — obwohl
			// real KEIN einziger ffmpeg-Prozess mehr lief.
			//
			// 🔴 KORREKTUR (noch selbiger Tag, 2026-09-17, User-Report
			// "Source error" bei AV1): der erste Fix behandelte JEDES
			// beendete ffmpeg gleich — auch ein ganz normal ERFOLGREICH
			// fertig transkodiertes Video (Dateiende erreicht, komplette
			// Playlist). Bei AV1 scheitert der Hardware-Decoder zuverlaessig
			// (Intel UHD 770 kann AV1 nicht via VAAPI), der CPU-Fallback
			// laeuft aber komplett durch — kein Fehler. Trotzdem loeschte
			// der GC das Session-Verzeichnis SOFORT, sobald Done() true war,
			// noch bevor der Client die letzten Segmente abgeholt hatte →
			// 404 auf gerade geloeschte Dateien, beim Client als
			// "Source error" sichtbar. Jetzt nur noch bei Failed() sofort
			// raus; ein regulaer beendeter Transcode faellt auf den
			// normalen Idle-Pfad zurueck (sessionIdleTimeout), der dem
			// Client genug Zeit laesst, fertig abzuspielen.
			//
			// `Done()`/`Failed()` sind nicht blockierend, kosten hier also
			// nichts.
			if s.Done() && s.Failed() {
				log.Printf("[transcode] session %s: mit Fehler beendet → aus dem Pool entfernt", id)
				s.Stop() // raeumt das Cache-Verzeichnis ab
				delete(m.sessions, id)
				continue
			}
			if idle > sessionIdleTimeout {
				log.Printf("[transcode] session %s idle %v → stop", id, idle)
				s.Stop()
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
	}
}

// StopSession beendet eine spezifische Session und entfernt sie aus dem Pool,
// falls sie existiert. Wird vom „Von Anfang"-Pfad genutzt: ohne Reset würde
// eine alte Session bei start=0 (von einem vorherigen Lauf, akkumuliertes
// Material) wiederverwendet — der Browser bekommt eine Playlist mit weit
// fortgeschrittenem Stand und springt nicht zu 0.
func (m *Manager) StopSession(itemID int64, profile Profile, audioIdx int, startSec float64, deinterlace bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dei := 0
	if deinterlace {
		dei = 1
	}
	id := fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profile.ID, audioIdx, int(startSec), dei)
	if s, ok := m.sessions[id]; ok {
		log.Printf("[transcode] session %s manuell gestoppt (fresh)", id)
		s.Stop()
		delete(m.sessions, id)
	}
}

// StopAllForItem beendet JEDE laufende Transcode-Session eines Items,
// unabhängig von Profil/Start-Offset — aufgerufen wenn der Client aktiv
// „Wiedergabe beendet" meldet (POST /playback/{id}/stop). Ohne das lief eine
// gerade geschlossene Session bis zu 30 Minuten (sessionIdleTimeout, s. o.)
// mit voller Encoder-Last weiter, obwohl niemand mehr zusieht — ffmpeg
// transcodiert ohne Gegendruck vom Client so schnell wie die Hardware
// hergibt, nicht nur in Echtzeit. Live beobachtet (2026-09-14): zwei
// Sessions liefen >500 % CPU je, mehrere Minuten nachdem der Client den
// Stop bereits gemeldet hatte, weil bis dahin nur geloggt, aber nie
// gestoppt wurde.
func (m *Manager) StopAllForItem(itemID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		if s.ItemID == itemID {
			log.Printf("[transcode] session %s gestoppt (Client meldet Wiedergabe-Ende)", id)
			s.Stop()
			delete(m.sessions, id)
		}
	}
	// Live beobachtet (2026-09-14, direkt nach der ersten StopAllForItem-
	// Version): eine bereits im Flug befindliche Playlist-/Segment-Anfrage
	// der GERADE geschlossenen Session (Netzwerk-Race, z. B. GStreamer
	// puffert beim Umschalten noch einen Request voraus) traf oft SOFORT
	// (~1s) NACH diesem Stop hier ein und erzeugte über StartOrGet eine
	// neue Session desselben Items — die dann NIE wieder einen Stop bekam
	// (der Client hat dieses Item bereits als geschlossen abgehakt) und bis
	// zum 30-Min-Idle-GC mit voller Last weiterlief. `stoppedAt` markiert
	// den Zeitpunkt; StartOrGet verweigert eine echte Neu-Erzeugung fuer
	// dieses Item innerhalb von stopSuppressWindow.
	m.stoppedAt[itemID] = time.Now()
}

// SessionAge liefert die Lebensdauer der existierenden Session zur Key oder
// (false, _), wenn keine läuft. Wird vom Playlist-Handler genutzt, um
// `fresh=1` idempotent zu machen: VHS holt EVENT-Playlists periodisch neu
// mit derselben URL — würde der Server jedes Mal die ffmpeg-Session killen,
// käme nie ein zweites Segment beim Player an, Playback hängt nach 4 s.
// Akzeptiert wird `fresh` nur, wenn die Session noch nicht existiert oder
// älter als ein paar Sekunden ist (bereits genug Material produziert hat).
func (m *Manager) SessionAge(itemID int64, profile Profile, audioIdx int, startSec float64, deinterlace bool) (bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dei := 0
	if deinterlace {
		dei = 1
	}
	id := fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profile.ID, audioIdx, int(startSec), dei)
	if s, ok := m.sessions[id]; ok {
		return true, time.Since(s.StartedAt)
	}
	return false, 0
}

// ConsumeFresh meldet, ob ein `fresh=1`-Request fuer diesen Session-Key gerade
// JETZT ausgefuehrt werden darf. Discriminator ist der `_t`-Token aus der
// URL (Date.now() vom Frontend, eindeutig pro applyPlayback-Aufruf):
//   - Gleicher Token wie zuletzt → VHS-Reload derselben Wiedergabe → false
//     (Session bleibt am Leben).
//   - Anderer/leerer Token → echter neuer Player-Open → true (Caller killt
//     ggf. eine alte Session aus einer frueheren Wiedergabe und startet neu).
//
// Frueher war die Idempotenz ueber ein 60-s-Zeitfenster realisiert. Das
// hat den fundamentalen Bug erzeugt: VHS laedt die EVENT-Playlist
// permanent mit fresh=1, nach 60 s lief das Fenster ab → die naechste
// Reload triggerte Stop+Restart der laufenden ffmpeg-Session →
// Wiedergabe stallte alle ~60 s. Der `_t`-Token koppelt die Idempotenz
// an die tatsaechliche Player-Session statt an Wallclock.
func (m *Manager) ConsumeFresh(itemID int64, profile Profile, audioIdx int, startSec float64, deinterlace bool, freshToken string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	dei := 0
	if deinterlace {
		dei = 1
	}
	id := fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profile.ID, audioIdx, int(startSec), dei)
	if last, ok := m.freshTokens[id]; ok && last == freshToken && freshToken != "" {
		return false
	}
	m.freshTokens[id] = freshToken
	// Map gelegentlich aufraeumen, falls sehr viele unique Session-Keys
	// auflaufen — Standardfall hat dutzende Eintraege, sehr klein.
	if len(m.freshTokens) > 500 {
		// Einfache Strategie: alle Eintraege verwerfen, deren Session nicht
		// (mehr) existiert. Token-Drosselung wird damit fuer veraltete
		// Eintraege resettet, das ist OK — die zugehoerige Session ist eh weg.
		for k := range m.freshTokens {
			if _, alive := m.sessions[k]; !alive {
				delete(m.freshTokens, k)
			}
		}
	}
	return true
}

// DiagnoseItem beschreibt den Zustand aller Transcode-Sessions eines Items in
// einer Zeile. Gedacht fuer den Fehlerpfad: meldet ein Client einen
// Wiedergabefehler, haelt der Server damit fest, wie es in genau diesem Moment
// serverseitig aussah. Ohne das ist der Fall spaeter nicht mehr
// rekonstruierbar — der GC raeumt die Session binnen Minuten ab, und dann
// steht nur noch die (generische) Client-Meldung im Protokoll.
//
// Bewusst reine Diagnose: liest nur, veraendert nichts, und schluckt jeden
// Fehler beim Verzeichnis-Lesen — ein Diagnose-Aufruf darf den Fehlerpfad
// niemals seinerseits zum Scheitern bringen.
func (m *Manager) DiagnoseItem(itemID int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := strconv.FormatInt(itemID, 10) + "-"
	var parts []string
	for id, s := range m.sessions {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		alive := true
		select {
		case <-s.done:
			alive = false
		default:
		}
		segs, playlist := 0, false
		if entries, err := os.ReadDir(s.Dir); err == nil {
			for _, e := range entries {
				switch {
				case strings.HasSuffix(e.Name(), ".ts"):
					segs++
				case e.Name() == "index.m3u8":
					playlist = true
				}
			}
		}
		parts = append(parts, fmt.Sprintf(
			"session=%s alter=%s ffmpeg_laeuft=%v playlist=%v segmente=%d",
			id, m.round(time.Since(s.StartedAt)), alive, playlist, segs))
	}
	if len(parts) == 0 {
		return "keine aktive Transcode-Session (Direct Play, oder Session bereits beendet)"
	}
	sort.Strings(parts) // stabile Reihenfolge, damit zwei Berichte vergleichbar sind
	return strings.Join(parts, " | ")
}

// round kuerzt eine Dauer auf Sekunden — in der Diagnose sind Nanosekunden
// nur Rauschen.
func (m *Manager) round(d time.Duration) string {
	return d.Truncate(time.Second).String()
}

// LookupSession liefert die existierende Session zur Key oder nil, wenn keine
// läuft. Im Gegensatz zu StartOrGet wird KEINE neue Session erzeugt — der
// Progress-Handler nutzt das, damit ein Progress-Poll mit nicht ganz exakt
// passenden Parametern (z.B. veraltetem `start=`) nicht versehentlich eine
// zweite ffmpeg-Instanz parallel zur eigentlichen Playback-Session startet.
// Eine konkurrierende ffmpeg-Instanz wuerde sich mit der laufenden Wiedergabe
// um die iGPU/CPU streiten und Stutter erzeugen.
func (m *Manager) LookupSession(itemID int64, profile Profile, audioIdx int, startSec float64, deinterlace bool) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	dei := 0
	if deinterlace {
		dei = 1
	}
	id := fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profile.ID, audioIdx, int(startSec), dei)
	if s, ok := m.sessions[id]; ok {
		s.Touch()
		return s
	}
	return nil
}

// sessionKey bildet den Schluessel einer Transcode-Sitzung. Bewusst EINE
// Stelle: Verzeichnisname, Map-Schluessel und Tests muessen exakt dieselbe
// Formel benutzen (der Startup-Cleanup matcht per sessionDirPattern darauf).
func sessionKey(itemID int64, profileID string, audioIdx int, startSec float64, deinterlace bool) string {
	dei := 0
	if deinterlace {
		dei = 1
	}
	return fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profileID, audioIdx, int(startSec), dei)
}

// StartOrGet returns an existing session for the item or starts a new one.
// Sessions werden pro (Item, Profil, Audio-Stream, Start-Offset, Deinterlace) gehalten.
// audioIdx = -1 → default (erster Audio-Stream). Sonst ffprobe-Stream-Index.
// deinterlace = true → backend-spezifischer Deinterlace-Filter wird in die
// Filter-Chain eingebaut (für interlaced Content wie alte TV-Captures).
// audioOnly: true für Musik-Items ohne Video-Stream (kind=music) — buildArgs
// überspringt dann komplett den Video-Filter/Hwaccel-Zweig. Ändert NICHT die
// Session-ID-Zusammensetzung (die bleibt wie gehabt aus itemID/profile/
// audioIdx/startSec/deinterlace), da audioOnly für ein gegebenes Item immer
// gleich ist — StopSession/SessionAge/ConsumeFresh/LookupSession brauchen
// den Parameter deshalb nicht. audioOnly zaehlt allerdings NICHT gegen das
// Transcode-Limit (siehe activeVideoSessionsLocked).
//
// videoCodec (ffprobe-Codec-Name, z.B. "av1"/"hevc"/""): wird NUR benutzt, um
// die Startstufe zu waehlen (siehe initialStageFor) — kein Einfluss auf den
// Session-Key.
func (m *Manager) StartOrGet(itemID int64, inputPath string, profile Profile, audioIdx int, startSec float64, deinterlace bool, audioOnly bool, srcHeight int, videoCodec string) (*Session, error) {
	return m.StartOrGetVOD(itemID, inputPath, profile, audioIdx, startSec, deinterlace, audioOnly, srcHeight, videoCodec, nil, 0)
}

// StartOrGetVOD wie StartOrGet, legt eine NEUE Sitzung aber mit VOD-Plan an
// (plan != nil, siehe vod.go), deren erster ffmpeg-Lauf bei Segment startSeg
// beginnt. Eine bestehende Sitzung wird unverändert zurückgegeben — egal in
// welchem Modus sie läuft; der Aufrufer richtet sich nach Session.VOD().
func (m *Manager) StartOrGetVOD(itemID int64, inputPath string, profile Profile, audioIdx int, startSec float64, deinterlace bool, audioOnly bool, srcHeight int, videoCodec string, plan *VODPlan, startSeg int) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := sessionKey(itemID, profile.ID, audioIdx, startSec, deinterlace)
	if s, ok := m.sessions[id]; ok {
		s.Touch()
		return s, nil
	}
	// stopSuppressWindow: verweigert eine echte Neu-Erzeugung kurz nach einem
	// per StopAllForItem gemeldeten expliziten Client-Stop desselben Items —
	// siehe Kommentar dort. 3s deckt die beobachtete Race (~1s) komfortabel
	// ab, ohne einen absichtlichen sofortigen Replay durch den User spuerbar
	// zu blockieren (in der Praxis nie so schnell erneut angeklickt).
	const stopSuppressWindow = 3 * time.Second
	if t, ok := m.stoppedAt[itemID]; ok && time.Since(t) < stopSuppressWindow {
		return nil, ErrStoppedRecently
	}
	// 🔴 2026-09-13: Ein erster Versuch, hier andere Sessions DESSELBEN Items
	// SOFORT zu stoppen, wurde noch am selben Tag wieder entfernt — Live-
	// Diagnose zeigte, dass der Mac-App-Client beim Player-Start teils
	// wiederholt ZWEI verschiedene Playlist-Requests (start=0 UND die echte
	// Resume-Position) im Sekundentakt hintereinander schickt. Ein SOFORTIGES
	// gegenseitiges Stoppen fuehrte dabei zu einem sich selbst verstaerkenden
	// Ping-Pong (Session A stoppt B, B's naechster Request stoppt A, …), bei
	// dem NIE eine Playlist fertig wird, bevor die Session schon wieder
	// gekillt ist — Client bekam HTTP 404/500 statt Video.
	//
	// 🔴→✅ 2026-09-14: Ohne JEDE Aufraeumung haeufte sich das Gegenteil an —
	// Live-Diagnose nach einem User-Report ("Container haengt, 60%+ Last")
	// fand per `docker top` ELF gleichzeitig laufende hw=true-ffmpeg-Prozesse
	// fuer nur eine Handvoll Items (u. a. FUENF parallele Sessions fuer
	// dasselbe Video bei verschiedenen Start-Offsets 0/286/680/1074/1648 —
	// jeder Sitzungswechsel durch Spulen im Player liess die vorherige
	// Session einfach 5 Minuten lang unbeaufsichtigt weiterlaufen, jede fuer
	// sich ein volles Hardware-Encode). Kompromiss statt der beiden Extreme:
	// eine andere Session DESSELBEN Items wird nur gestoppt, wenn sie bereits
	// laenger als `siblingStopGracePeriod` lebt — jung genug, dass eine
	// schnelle Doppelanfrage-Race (wie beim Ping-Pong oben) nicht dazwischen-
	// funkt, aber weit unter dem 5-Minuten-GC, damit echtes Spulen im Player
	// nicht mehr auf Kosten von Bergen paralleler Encodes geht.
	const siblingStopGracePeriod = 10 * time.Second
	for otherID, other := range m.sessions {
		if other.ItemID == itemID && otherID != id && time.Since(other.StartedAt) >= siblingStopGracePeriod {
			log.Printf("[transcode] session %s gestoppt (abgeloest durch neue Session %s desselben Items, alter=%s)", otherID, id, time.Since(other.StartedAt).Round(time.Second))
			other.Stop()
			delete(m.sessions, otherID)
		}
	}
	// Tote Sitzungen ausbuchen, BEVOR das Budget geprüft wird (seit
	// 2026-09-17). Der GC-Lauf tut das ebenfalls, aber nur einmal pro Minute
	// — in dieser Lücke blockierten gescheiterte Versuche sonst weiter das
	// Budget. Genau das führte am 2026-09-17 zu „Sitzungs-Obergrenze
	// erreicht (12)", obwohl real kein einziger ffmpeg-Prozess mehr lief.
	//
	// 🔴 NUR bei Failed() — derselbe Grund wie im GC-Loop oben (Korrektur
	// noch selbiger Tag, User-Report "Source error" bei AV1): eine
	// erfolgreich beendete Session darf hier nicht vorzeitig verschwinden,
	// nur weil zufällig zeitgleich eine neue Wiedergabe startet.
	for otherID, other := range m.sessions {
		if other.Done() && other.Failed() {
			log.Printf("[transcode] session %s: mit Fehler beendet → Platz freigegeben", otherID)
			other.Stop()
			delete(m.sessions, otherID)
		}
	}
	// 🔴 Gewichtetes Budget gleichzeitiger Video-Transcodes (seit 2026-09-17).
	//
	// Bis hierher konnte JEDE Anfrage bedingungslos einen weiteren
	// ffmpeg-Prozess starten. Beim User-Test am 2026-09-16 (wie viele
	// gleichzeitige 4K-Transcodes schafft die iGPU?) liefen VIER stabil,
	// bei ACHT riss es den GESAMTEN Unraid-Host mit — kompletter Reboot,
	// nicht nur ein Container-Neustart. Ein abgelehnter Film ist immer
	// besser als ein toter Server, deshalb hier ein hartes Nein statt
	// „irgendwie noch reinquetschen".
	//
	// Gezaehlt werden NICHT Sitzungen, sondern KOSTENPUNKTE (siehe
	// transcodeCost): eine 4K→4K-Umwandlung kostet das volle Budget-Mass,
	// ein 480p-Stream aus kleiner Quelle nur einen Bruchteil. Sonst
	// blockierte eine winzige Umwandlung denselben Platz wie eine 4K-Last
	// und im Alltag bliebe Kapazitaet ungenutzt.
	//
	// Bewusste Details:
	//   - Der Check steht NACH dem Sibling-Cleanup oben: dort gerade
	//     freigewordene Plaetze zaehlen bereits mit, sonst wuerde ein
	//     simpler Seek im Player faelschlich am Limit scheitern.
	//   - Der Check steht NACH dem `m.sessions[id]`-Treffer ganz oben: eine
	//     BESTEHENDE Session weiterzubenutzen ist nie limitiert, sonst
	//     briche ein laufender Film beim naechsten Playlist-Reload ab.
	//   - Nur Video zaehlt (siehe activeCostLocked) — Musik soll keinen
	//     Filmplatz wegnehmen.
	//   - Zusaetzlich ein Deckel auf die ANZAHL (maxSessionsHardCapFactor):
	//     lauter billige Sitzungen wuerden sonst rechnerisch ueber zwanzig
	//     ffmpeg-Prozesse erlauben, die zwar die Grafikeinheit kaum
	//     belasten, aber je Prozess Speicher und Dateihandles kosten.
	if m.maxSessions > 0 && !audioOnly {
		budget := m.maxSessions * CostFullBudgetUnit
		cost := transcodeCost(srcHeight, profile.MaxHeight)
		if active := m.activeCostLocked(); active+cost > budget {
			log.Printf("[transcode] ABGELEHNT item=%d: Budget erschoepft (%d+%d von %d Punkten, %d Sitzungen aktiv)",
				itemID, active, cost, budget, m.activeVideoSessionsLocked())
			return nil, fmt.Errorf("%w (Auslastung %d%%)", ErrTooManySessions, active*100/budget)
		}
		if n := m.activeVideoSessionsLocked(); n >= m.maxSessions*maxSessionsHardCapFactor {
			log.Printf("[transcode] ABGELEHNT item=%d: Sitzungs-Obergrenze erreicht (%d)", itemID, n)
			return nil, fmt.Errorf("%w (%d gleichzeitige Umwandlungen)", ErrTooManySessions, n)
		}
	}
	// Verzeichnis-Name = Session-Key + eindeutiger Suffix. Der Suffix ist
	// ESSENTIELL, nicht kosmetisch: `Stop()` wartet nur 3 s auf das Ende von
	// ffmpeg und loescht danach `s.Dir` — ein langsam sterbender Prozess
	// (HEVC-Decode via VAAPI braucht gelegentlich laenger) schreibt danach
	// weiter. Ohne Suffix legt `StartOrGet` fuer denselben Session-Key
	// unmittelbar DENSELBEN Pfad neu an: der alte ffmpeg schreibt dann in das
	// Verzeichnis der neuen Session, beide ueberschreiben wechselseitig
	// index.m3u8 und die seg*.ts-Nummern kollidieren. Der Client bekommt eine
	// korrupte Playlist und meldet einen Datenstromfehler — bei `fresh=1`
	// (Seek / neuer Player-Open) genau der Pfad, der das ausloest.
	return m.startLocked(id, sessionSpec{
		itemID:      itemID,
		inputPath:   inputPath,
		profile:     profile,
		audioIdx:    audioIdx,
		startSec:    startSec,
		deinterlace: deinterlace,
		audioOnly:   audioOnly,
		srcHeight:   srcHeight,
		vod:         plan,
		vodStartSeg: startSeg,
	}, m.initialStageFor(videoCodec))
}

// initialStageFor waehlt die Startstufe anhand des Quell-Codecs. Normalfall
// ist stageHardware (VAAPI dekodiert UND encodiert). Ausnahme: AV1 auf
// VAAPI-Hardware scheitert dort IMMER (Intel-iGPU/iHD-Treiber kann laut
// vainfo kein AV1 decodieren, siehe Skill goldfish-playback) — der erste
// Versuch wuerde jedesmal mit „Failed to inject frame into filter network:
// Function not implemented" abbrechen und erst danach per RetryWithFallback
// auf stageCPUEncodeVAAPI zurueckfallen. Das kostete bisher IMMER einen
// vollstaendigen ffmpeg-Fehlversuch (Log-Rauschen, ~1s extra Verzoegerung)
// fuer einen Fall, der vorher schon feststeht. Bei AV1 auf VAAPI startet die
// Sitzung deshalb direkt auf stageCPUEncodeVAAPI — spart den unnoetigen
// ersten Versuch, das Verhalten fuer den Client aendert sich nicht (gleicher
// Zielzustand, nur ohne den dazwischenliegenden Fehlschlag). Andere Backends
// (NVENC/Software) und alle anderen Codecs bleiben unveraendert bei
// stageHardware.
func (m *Manager) initialStageFor(videoCodec string) fallbackStage {
	if m.hw.Selected == BackendVAAPI && strings.EqualFold(videoCodec, "av1") {
		return stageCPUEncodeVAAPI
	}
	return stageHardware
}

// RetryWithFallback setzt eine gescheiterte Sitzung noch einmal auf — auf der
// jeweils nächsten Rückfallstufe.
//
// Noetig seit der Umstellung auf Hardware-Decode (2026-09-14): scheitert die
// Grafikeinheit an einer Datei — exotischer oder beschaedigter Datenstrom, ein
// Codec, den der Decoder dieser Hardware nicht kann —, liefert ffmpeg gar
// keine Playlist und die Wiedergabe waere tot.
//
// Zwei Stufen, in dieser Reihenfolge (2026-09-22):
//
//  1. `stageCPUEncodeVAAPI` — CPU dekodiert, die Grafikeinheit encodiert.
//     Nur auf VAAPI-Hardware sinnvoll. Gemessen mit AV1: 38,6 s CPU-Zeit
//     (reiner Software-Weg) gegen 11,0 s — Faktor 3,5 bei identischem
//     Ergebnis. Kosten: die Bilder gehen einmal durch den Hauptspeicher.
//  2. `stageFullSoftware` — CPU dekodiert und encodiert (libx264). Letzte
//     Stufe; sie muss es für JEDEN Codec geben, denn sie ist der einzige Weg,
//     der nie an der Hardware scheitern kann. (Kein Rückschritt zum früheren
//     „halben" Rückfall: der versuchte bei einem Codec, den der DECODER nicht
//     kann, trotzdem `hwupload` + `h264_vaapi` und scheiterte mit derselben
//     Meldung — hier läuft der DECODE per CPU, der ENCODE über die Hardware,
//     und der Encoder kann H.264 immer.)
//
// Genau EINE Stufe pro Aufruf: eine Sitzung auf der letzten Stufe wird
// abgelehnt. Sonst entstuende bei einer wirklich kaputten Datei eine
// Endlosschleife aus Neustarts.
func (m *Manager) RetryWithFallback(s *Session) (*Session, error) {
	if s == nil {
		return nil, errors.New("keine Sitzung")
	}
	m.mu.Lock()
	if s.stage == stageFullSoftware {
		m.mu.Unlock()
		return nil, errors.New("läuft bereits vollständig per CPU")
	}
	if cur, ok := m.sessions[s.ID]; !ok || cur != s {
		// Inzwischen abgeloest (Seek, GC, anderer Player) — dann ist diese
		// Sitzung nicht mehr unser Problem.
		m.mu.Unlock()
		return nil, errors.New("Sitzung nicht mehr aktuell")
	}
	next := stageFullSoftware
	if s.stage == stageHardware && m.hw.Selected == BackendVAAPI && m.hw.VAAPIDevice != "" {
		next = stageCPUEncodeVAAPI
	}
	delete(m.sessions, s.ID)
	id, spec := s.ID, s.spec
	m.mu.Unlock()

	// Stop() wartet bis zu 3 s auf das Ende von ffmpeg — bewusst OHNE Mutex,
	// sonst blockiert das jede andere Sitzung so lange mit.
	s.Stop()

	stageName := map[fallbackStage]string{
		stageCPUEncodeVAAPI: "CPU-Decode + Grafikeinheit-Encode",
		stageFullSoftware:   "vollständig per CPU",
	}[next]
	log.Printf("[transcode] session %s: Hardware-Decode gescheitert, neuer Versuch %s", id, stageName)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(id, spec, next)
}

// sessionDirCounter macht Verzeichnisnamen garantiert eindeutig.
//
// 🔴 Der Suffix bestand urspruenglich NUR aus `time.Now().UnixNano()` — das
// ist NICHT kollisionsfrei: die Uhr-Aufloesung ist plattformabhaengig (auf
// macOS grob genug, dass zwei unmittelbar aufeinanderfolgende Aufrufe
// denselben Wert liefern; `TestSessionDirsAreUniquePerStart` schlug deshalb
// reproduzierbar fehl). Genau dann entsteht wieder der Zustand, gegen den
// der Suffix 2026-09-13 eingefuehrt wurde: ein noch sterbendes ffmpeg
// (Stop() wartet nur 3 s) und ein neu gestartetes schreiben in DASSELBE
// Verzeichnis, ueberschreiben wechselseitig index.m3u8 und vergeben
// seg*.ts-Nummern doppelt → korrupte Playlist, Datenstromfehler im Client.
// Ein monoton steigender Zaehler schliesst das unabhaengig von der
// Uhr-Aufloesung aus.
var sessionDirCounter atomic.Uint64

// sessionDirName bildet den Verzeichnisnamen einer Session: Session-Key +
// garantiert eindeutiger Suffix. Muss zu `sessionDirPattern` passen (sonst
// raeumt der Startup-Cleanup die Verzeichnisse nie weg — siehe dort).
func sessionDirName(id string) string {
	return id + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) +
		strconv.FormatUint(sessionDirCounter.Add(1), 36)
}

// startLocked legt Verzeichnis und ffmpeg-Prozess an und traegt die Sitzung
// ein. Der Aufrufer MUSS m.mu halten.
func (m *Manager) startLocked(id string, spec sessionSpec, stage fallbackStage) (*Session, error) {
	dir := filepath.Join(m.cacheDir, sessionDirName(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return m.launchLocked(id, spec, stage, dir)
}

// launchLocked startet ffmpeg in dir und traegt die Sitzung ein. Getrennt von
// startLocked, weil ein VOD-Neustart (restartVOD) bewusst im SELBEN
// Verzeichnis weiterschreibt. Der Aufrufer MUSS m.mu halten.
func (m *Manager) launchLocked(id string, spec sessionSpec, stage fallbackStage, dir string) (*Session, error) {
	ctx, cancel := context.WithCancel(context.Background())
	args := m.buildArgs(spec.inputPath, dir, spec.profile, spec.audioIdx, spec.startSec,
		spec.deinterlace, spec.audioOnly, stage, spec.vod, spec.vodStartSeg)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	// stderr NICHT verwerfen: ffmpeg laeuft mit `-loglevel error`, hier landet
	// also nur echtes Fehlerhaftes — und genau das fehlte bisher komplett im
	// Log, wenn eine Wiedergabe scheiterte. Der Ring-Puffer haelt die letzten
	// Zeilen; ausgegeben werden sie nur, wenn der Prozess mit Fehler endet
	// (ein per Kontext gekillter Prozess ist der Normalfall und schweigt).
	errBuf := &ringBuffer{max: 4096}
	cmd.Stderr = errBuf
	cmd.Stdout = io.Discard

	log.Printf("[transcode] start session=%s profile=%s audio=%d hw=%v decode=%s start=%.1fs deinterlace=%v",
		id, spec.profile.ID, spec.audioIdx, m.hw.Available,
		map[fallbackStage]string{stageHardware: "hardware", stageCPUEncodeVAAPI: "cpu-decode+vaapi-encode", stageFullSoftware: "cpu"}[stage], spec.startSec, spec.deinterlace)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	now := time.Now()
	s := &Session{
		ID:        id,
		ItemID:    spec.itemID,
		Profile:   spec.profile.ID,
		AudioIdx:  spec.audioIdx,
		Dir:       dir,
		StartSec:  spec.startSec,
		StartedAt: now,
		Cmd:       cmd,
		cancel:    cancel,
		lastUsed:  now,
		done:      make(chan struct{}),
		spec:      spec,
		stage:     stage,
	}
	go func() {
		err := cmd.Wait()
		// ⚠ VOR close(s.done) setzen — GC/StartOrGet lesen Done()==true als
		// Signal "kann ausgewertet werden" und müssen `failed` dann bereits
		// korrekt vorfinden (siehe Kommentar am Feld). Unter s.mu, weil
		// GC/StartOrGet aus einer anderen Goroutine lesen (Failed()).
		if err != nil && ctx.Err() == nil {
			s.mu.Lock()
			s.failed = true
			s.mu.Unlock()
		}
		close(s.done)
		// Kontext-Abbruch = gewolltes Stop (fresh/GC), das ist kein Fehler.
		if err != nil && ctx.Err() == nil {
			if out := errBuf.String(); out != "" {
				log.Printf("[transcode] session %s ffmpeg beendet mit %v: %s", id, err, out)
			} else {
				log.Printf("[transcode] session %s ffmpeg beendet mit %v", id, err)
			}
		}
	}()
	m.sessions[id] = s
	return s, nil
}

// buildArgs baut die ffmpeg-Kommandozeile einer Transcode-Sitzung.
//
// `stage` bestimmt den Weg: stageHardware (Normalfall seit 2026-09-14, VAAPI
// dekodiert UND encodiert) oder eine der beiden Rückfallstufen, die per CPU
// dekodieren — siehe `Manager.RetryWithFallback`.
func (m *Manager) buildArgs(input, outDir string, p Profile, audioIdx int, startSec float64, deinterlace, audioOnly bool, stage fallbackStage, vod *VODPlan, vodStartSeg int) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	if audioOnly {
		vod = nil // Musik laeuft nie als VOD (siehe PlanVOD)
	}
	// VOD-Neustart ab Segment k: die Quelle ab dessen Beginn lesen.
	if vod != nil {
		startSec += vod.SegStart(vodStartSeg)
	}

	if startSec > 0 {
		args = append(args, "-ss", strconv.FormatFloat(startSec, 'f', 3, 64))
	}

	// Musik (kind=music, kein Video-Stream): eigener, komplett unabhängiger
	// Zweig VOR der Video-Filter/Hwaccel-Verzweigung unten — kein -vf, kein
	// Hwaccel-Device-Init, direkt Audio-only-HLS. Gleiche HLS-Mux-Konvention
	// wie der Video-Pfad (Segment-Dauer/Playlist-Type), nur ohne alles
	// Video-Spezifische.
	if audioOnly {
		args = append(args, "-i", input, "-map", "0:a:0")
		audioKbps := p.AudioKbps
		if audioKbps <= 0 {
			audioKbps = 192
		}
		args = append(args,
			"-c:a", "aac",
			"-b:a", fmt.Sprintf("%dk", audioKbps),
			"-f", "hls",
			"-hls_time", "4",
			"-hls_list_size", "0",
			"-hls_flags", "independent_segments",
			"-hls_playlist_type", "event",
			"-hls_segment_type", "mpegts",
			"-hls_segment_filename", filepath.Join(outDir, "seg%05d.ts"),
			filepath.Join(outDir, "index.m3u8"),
		)
		return args
	}

	// Video-Filter: Skalierung (CPU) + VAAPI-Upload. Software-scaling ist billig
	// und funktioniert mit beliebigen Input-Codecs.
	scaleFilter := ""
	if p.MaxHeight > 0 {
		scaleFilter = fmt.Sprintf("scale=-2:%d:force_original_aspect_ratio=decrease,", p.MaxHeight)
	}
	// CPU-Deinterlace-Filter (für NVENC- und Software-Pfad). bwdif liefert
	// minimal bessere Kanten als yadif, vergleichbare CPU-Last.
	cpuDeintFilter := ""
	if deinterlace {
		cpuDeintFilter = "bwdif,"
	}

	switch m.hw.Selected {
	case BackendVAAPI:
		if stage == stageCPUEncodeVAAPI {
			// RÜCKFALLSTUFE 1 (seit 2026-09-22): CPU dekodiert, die
			// Grafikeinheit encodiert.
			//
			// Gedacht für Dateien, an deren DEKODER die Hardware scheitert,
			// während der Encoder sie problemlos kann. Gemessen am 2026-09-22
			// mit AV1 (YouTube-Material, „Meadow House Diary"): die
			// Grafikeinheit kann AV1 nicht initialisieren (`vainfo` listet
			// zwar VAProfileAV1Profile0, ffmpeg scheitert aber mit
			// „Failed to inject frame into filter network: Function not
			// implemented"); im reinen Software-Weg kosteten 20 s Material
			// 38,6 s CPU-Zeit, auf diesem Weg 11,0 s — Faktor 3,5.
			//
			// Der Filterweg ist der CPU-Weg (bwdif/scale) plus
			// `format=nv12,hwupload` am Ende: erst in Software entflimmern und
			// skalieren, dann die Bilder einmal hochladen. `format=nv12` ist
			// Pflicht — `h264_vaapi` kann keine 10-Bit-Flächen encodieren.
			//
			// Bewusst OHNE `-hwaccel`: der Decode soll hier gerade nicht über
			// die Grafikeinheit laufen, sonst entsteht genau der Fehler, der
			// diese Stufe ausgelöst hat.
			videoFilter := cpuDeintFilter + scaleFilter + "format=nv12,hwupload"
			args = append(args,
				"-vaapi_device", m.hw.VAAPIDevice,
				"-i", input,
				"-vf", videoFilter,
				"-c:v", "h264_vaapi",
			)
			break
		}
		if stage == stageFullSoftware {
			// 🔴 VOLLSTÄNDIGER Software-Weg (seit 2026-09-17): dekodieren UND
			// encodieren per CPU, ohne jede Beteiligung der Grafikeinheit.
			//
			// Der frühere „Rückfall" dekodierte zwar per CPU, lud die Bilder
			// danach aber per `hwupload` wieder auf die Grafikeinheit und
			// encodierte mit `h264_vaapi` — für einen Codec, den die Hardware
			// gar nicht kennt, ist das kein Rückfall, sondern derselbe Fehler
			// mit einem Zwischenschritt. Live beobachtet am 2026-09-17 mit
			// einer WMV3-Datei (VC-1-Familie): `vainfo` listet auf dieser
			// Hardware KEIN VAProfileVC1* — Intel hat den VC-1-Decoder ab
			// Gen 12 gestrichen. ffmpeg meldete
			// „No support for codec wmv3 profile 1" und
			// „Failed setup for format vaapi", und zwar in BEIDEN Versuchen;
			// die Wiedergabe war damit tot statt langsam.
			//
			// libx264 mit `veryfast` schafft solche Dateien locker in
			// Echtzeit — sie sind typischerweise alt und klein (SD/720p).
			// Diese Sitzungen zählen im Budget wie jede andere; ihr echter
			// Aufwand liegt auf der CPU, wo genug Kerne frei sind.
			videoFilter := cpuDeintFilter + scaleFilter
			args = append(args, "-i", input)
			if videoFilter != "" {
				args = append(args, "-vf", strings.TrimSuffix(videoFilter, ","))
			}
			args = append(args,
				"-c:v", "libx264",
				"-preset", "veryfast",
				"-pix_fmt", "yuv420p",
			)
			if p.VideoKbps > 0 {
				args = append(args,
					"-b:v", fmt.Sprintf("%dk", p.VideoKbps),
					"-maxrate", fmt.Sprintf("%dk", p.VideoKbps*3/2),
					"-bufsize", fmt.Sprintf("%dk", p.VideoKbps*2),
				)
			} else {
				args = append(args, "-crf", "23")
			}
			break
		}
		{
			// Hardware-Decode (seit 2026-09-14). Vorher lief NUR das Encoden
			// auf der Grafikeinheit, dekodiert wurde per CPU — bei 4K-Material
			// der mit Abstand teuerste Teil. Am laufenden Server gemessen,
			// 60 s aus einem 3840x2160-HEVC, profile=orig, sonst im Leerlauf:
			//
			//     Software-Decode + hwupload (alt): 186 s CPU-Zeit, 33 s Wanduhr
			//     -hwaccel vaapi + scale_vaapi (neu):  6 s CPU-Zeit, 18 s Wanduhr
			//
			// Faktor 31. Genau dieser Fall (ein 4K-Remux bei profile=orig)
			// hielt eine einzelne Sitzung dauerhaft bei ~570 % CPU.
			//
			// Die Bilder bleiben die ganze Kette ueber Flaechen der
			// Grafikeinheit: erst entflimmern, dann skalieren, dann encoden —
			// kein Herunterladen in den Hauptspeicher, kein `hwupload`.
			// `format=nv12` erzwingt 8 Bit: eine 10-Bit-HDR-Quelle liefert
			// sonst 10-Bit-Flaechen, die `h264_vaapi` nicht encodieren kann
			// (dieselbe Notwendigkeit wie in internal/trickplay).
			//
			// Scheitert der Hardware-Decoder an einer Datei, springt
			// `RetryWithFallback` ein — ohne den waere die Wiedergabe
			// solcher Dateien tot, denn dieser Pfad hatte nie einen Rueckfall.
			chain := make([]string, 0, 2)
			if deinterlace {
				chain = append(chain, "deinterlace_vaapi=mode=motion_adaptive")
			}
			if p.MaxHeight > 0 {
				chain = append(chain, fmt.Sprintf(
					"scale_vaapi=w=-2:h=%d:force_original_aspect_ratio=decrease:format=nv12", p.MaxHeight))
			} else {
				// Ohne Groessenaenderung bleibt scale_vaapi trotzdem noetig —
				// allein wegen der 8-Bit-Wandlung.
				chain = append(chain, "scale_vaapi=format=nv12")
			}
			args = append(args,
				"-vaapi_device", m.hw.VAAPIDevice,
				"-hwaccel", "vaapi",
				"-hwaccel_output_format", "vaapi",
				"-i", input,
				"-vf", strings.Join(chain, ","),
				"-c:v", "h264_vaapi",
			)
			break
		}
		// Hinweis: Der frühere „halbe" Rückfallweg war am 2026-09-17 entfallen,
		// weil er bei einem Codec, den der DECODER nicht kann, trotzdem
		// `hwupload` + `h264_vaapi` versuchte und mit derselben Meldung
		// scheiterte. Seit 2026-09-22 gibt es ihn wieder — aber als EIGENE,
		// vorgelagerte Stufe (`stageCPUEncodeVAAPI`, siehe oben im
		// CPUEncodeVAAPI-Zweig) und nur dort, wo der Decode per CPU läuft.
		// Diese Stufe hier ist und bleibt der Weg für alles, was die
		// Grafikeinheit gar nicht kann (VC1/WMV3), und läuft deshalb ohne
		// jede Beteiligung der Hardware.
	case BackendNVENC:
		// NVENC-Pfad: `-hwaccel cuda` ohne `-hwaccel_output_format cuda` →
		// Frames werden nach dem Decode in den CPU-RAM kopiert, die CPU-
		// Scale-/Format-Filter laufen normal, ffmpeg lädt für den NVENC-
		// Encoder automatisch wieder hoch. Robuste Filter-Kompatibilität
		// mit allen Input-Codecs.
		videoFilter := cpuDeintFilter + scaleFilter
		if videoFilter != "" {
			videoFilter = strings.TrimSuffix(videoFilter, ",") // trailing Komma
		}
		args = append(args, "-hwaccel", "cuda", "-i", input)
		if videoFilter != "" {
			args = append(args, "-vf", videoFilter)
		}
		args = append(args,
			"-c:v", "h264_nvenc",
			"-preset", "p4",
			"-rc", "vbr",
			"-pix_fmt", "yuv420p",
		)
		if p.VideoKbps > 0 {
			args = append(args,
				"-b:v", fmt.Sprintf("%dk", p.VideoKbps),
				"-maxrate", fmt.Sprintf("%dk", p.VideoKbps*3/2),
				"-bufsize", fmt.Sprintf("%dk", p.VideoKbps*2),
			)
		} else {
			args = append(args, "-cq", "23")
		}
	default:
		// Software (libx264)
		videoFilter := cpuDeintFilter + scaleFilter
		if videoFilter != "" {
			videoFilter = strings.TrimSuffix(videoFilter, ",") // trailing Komma wegnehmen
		}
		args = append(args, "-i", input)
		if videoFilter != "" {
			args = append(args, "-vf", videoFilter)
		}
		args = append(args,
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-pix_fmt", "yuv420p",
		)
		if p.VideoKbps > 0 {
			args = append(args,
				"-b:v", fmt.Sprintf("%dk", p.VideoKbps),
				"-maxrate", fmt.Sprintf("%dk", p.VideoKbps*3/2),
				"-bufsize", fmt.Sprintf("%dk", p.VideoKbps*2),
			)
		} else {
			args = append(args, "-crf", "23")
		}
	}

	// Stream-Mapping: erstes ECHTES Video (Großbuchstabe V = ffmpeg-
	// Stream-Specifier "video, ohne attached pictures/Thumbnails/Cover-Art")
	// + ausgewählter Audio-Stream. Mit kleinem "v" hätte ein eingebettetes
	// Cover-Bild (disposition.attached_pic=1, z. B. ein mjpeg-Thumbnail in
	// WMV-Dateien) als "erster Video-Stream" gegolten und wäre statt des
	// echten Films transcodiert worden — Wiedergabe faktisch kaputt (Bug,
	// gefixt 2026-09-07, User-Report "Immer Ärger mit 40"/"Vielleicht
	// lieber morgen" spielen nicht ab + falsche 180p-Auflösung). Der
	// Scanner überspringt attached_pic-Streams bei der Metadaten-Erkennung
	// schon länger (siehe scanner.go); dieselbe Ausnahme fehlte hier beim
	// tatsächlichen Transcode-Mapping.
	args = append(args, "-map", "0:V:0")
	if audioIdx >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:%d", audioIdx))
	} else {
		args = append(args, "-map", "0:a:0?") // ? = optional, falls keine Audio-Spur
	}

	audioKbps := p.AudioKbps
	if audioKbps <= 0 {
		audioKbps = 160
	}
	args = append(args,
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", audioKbps),
		"-ac", "2",
		// Erzwinge Keyframe alle 2 s im Output. Ohne das schneidet
		// `-hls_time 2` Segmente nur an den Keyframes der QUELLE — und die
		// liegen oft 4–5 s auseinander. Dann wird das erste Segment trotz
		// hls_time=2 erst nach 4–5 s fertig. Mit `expr:gte(t,n_forced*2)`
		// setzt der Encoder beim ENCODEN selbst alle 2 s einen Keyframe.
		// Frame-Rate-unabhaengig, funktioniert bei VAAPI / NVENC / libx264.
		"-force_key_frames", "expr:gte(t,n_forced*2)",
		"-f", "hls",
		// Segment-Dauer 2 s: erstes Segment ist nach ~2 s startfaehig (statt
		// 4 s bei `hls_time=4`). Browser-Buffer-Anzeige aktualisiert sich
		// doppelt so haeufig, gefuehlte Latenz beim Play-Klick halbiert.
		// Kein nennenswerter Overhead — doppelt so viele kleine .ts-Files,
		// VHS reloadet die EVENT-Playlist statt alle 4 s nun alle 2 s
		// (`fresh=1`-Idempotenz hat ein 60 s-Fenster, also weiter unkritisch).
		"-hls_time", "2",
		"-hls_list_size", "0",
		// `independent_segments` erlaubt Segment-level Seek. `append_list`
		// ist BEWUSST NICHT dabei — ffmpeg würde sonst `#EXT-X-DISCONTINUITY`
		// direkt vor seg00000.ts einfügen, und VHS interpretiert das als
		// Lücke am Anfang → kein Buffer-Aufbau bei currentTime=0. Da wir die
		// Session mit `cleanDir` frisch starten, brauchen wir append_list nicht.
		"-hls_flags", "independent_segments",
		// EVENT-Playlist statt Live: Video.js behandelt sie als bounded
		// (kein Live-Edge-Snap beim Play nach Pause).
		"-hls_playlist_type", "event",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(outDir, "seg%05d.ts"),
		filepath.Join(outDir, "index.m3u8"),
	)
	if vod != nil {
		args = applyVODArgs(args, vod, vodStartSeg)
	}
	return args
}

// applyVODArgs stellt die HLS-Ausgabe auf vorab berechnete Segmente um
// (siehe vod.go): Keyframes nach BILDANZAHL statt nach Zeit, damit jedes
// Segment exakt vod.SegDur lang ist; `temp_file`, damit ein Segment erst nach
// dem Fertigschreiben unter seinem Namen auftaucht (der Segment-Handler
// liefert, was existiert); nach einem Neustart ab Segment k die Zeitstempel
// um dessen Beginn verschieben und ab k nummerieren. ffmpegs eigene
// index.m3u8 bleibt EVENT — sie dient nur noch dem Fortschritt.
func applyVODArgs(args []string, vod *VODPlan, startSeg int) []string {
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-force_key_frames":
			args[i+1] = fmt.Sprintf("expr:gte(n,n_forced*%d)", vod.FramesPerSeg)
		case "-hls_time":
			// Knapp unter der Segmentdauer: geschnitten wird am naechsten
			// Keyframe danach, also exakt an der Bildgrenze.
			args[i+1] = strconv.FormatFloat(vod.SegDur*0.95, 'f', 3, 64)
		case "-hls_flags":
			args[i+1] += "+temp_file"
		}
	}
	out := args[len(args)-1]
	extra := []string{"-start_number", strconv.Itoa(startSeg)}
	if startSeg > 0 {
		extra = append(extra, "-output_ts_offset", strconv.FormatFloat(vod.SegStart(startSeg), 'f', 6, 64))
	}
	return append(append(args[:len(args)-1:len(args)-1], extra...), out)
}

func (s *Session) Touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
	TouchActivity()
}

func (s *Session) Stop() {
	s.stopProcess()
	if s.Dir != "" {
		_ = os.RemoveAll(s.Dir)
	}
}

// stopProcess beendet ffmpeg (wartet bis zu 3 s), laesst das Verzeichnis aber
// stehen — ein VOD-Neustart schreibt dort weiter (siehe restartVOD).
func (s *Session) stopProcess() {
	// `cancel` kann fehlen, wenn eine Sitzung nicht über `startLocked`
	// entstanden ist (Tests, künftige Sonderpfade). Ein nil-Aufruf wäre ein
	// Absturz des ganzen Servers — für eine reine Aufräumfunktion ein
	// unnötiges Risiko.
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
		}
	}
}

// VOD liefert den VOD-Plan der Sitzung (nil = EVENT-Playlist).
func (s *Session) VOD() *VODPlan { return s.spec.vod }

// Position gibt zurück, bis zu welcher Quelldatei-Sekunde ffmpeg transcodiert hat.
// Wird aus der Summe der #EXTINF-Dauern in index.m3u8 + StartSec berechnet.
// Verwendung: Client kann ahead = Position - currentTime anzeigen.
func (s *Session) Position() (float64, error) {
	// VOD: ffmpegs index.m3u8 zaehlt nur den LAUFENDEN Lauf, der nach einem
	// Neustart bei Segment k beginnt.
	if s.spec.vod != nil {
		if _, err := os.Stat(filepath.Join(s.Dir, "index.m3u8")); err != nil {
			return 0, err
		}
		return s.StartSec + s.spec.vod.SegStart(s.vodProduced()), nil
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "index.m3u8"))
	if err != nil {
		return 0, err
	}
	total := 0.0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		rest := line[len("#EXTINF:"):]
		if c := strings.IndexByte(rest, ','); c >= 0 {
			rest = rest[:c]
		}
		if d, err := strconv.ParseFloat(rest, 64); err == nil {
			total += d
		}
	}
	return s.StartSec + total, nil
}

// Done zeigt an, ob der ffmpeg-Prozess bereits beendet ist (Transcode abgeschlossen
// oder abgebrochen). Der Client kann damit die Progress-Anzeige verstecken.
func (s *Session) Done() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// Failed meldet, ob die Sitzung mit einem ECHTEN ffmpeg-Fehler endete (Exit-
// Code != 0), im Unterschied zu einem regulären Abschluss (Dateiende erreicht)
// oder unserem eigenen Stop()/Context-Abbruch. Nur im ersten Fall darf der
// GC/StartOrGet die Sitzung SOFORT nach Done()==true entfernen — sonst würde
// ein ganz normal fertig transkodiertes Video (z. B. der CPU-Fallback bei
// AV1, siehe Kommentar am `failed`-Feld) seine Segmente verlieren, bevor der
// Client sie abgeholt hat. Nur sinnvoll, NACHDEM Done() true ist — vorher ist
// `failed` per Definition noch nicht gesetzt.
func (s *Session) Failed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

// WaitForPlaylist blocks until the playlist file exists (up to timeout).
// ErrFFmpegDiedEarly: der Prozess hat aufgegeben, BEVOR ueberhaupt eine
// Playlist entstand. Das unterscheidet einen echten Fehlschlag (Codec, den die
// Hardware nicht kann; beschaedigte Datei) von einem blossen Zeitueberlauf, bei
// dem ffmpeg noch arbeitet. Nur beim echten Fehlschlag lohnt der Rueckfall auf
// CPU-Decode — bei einem Zeitueberlauf wuerde ein Neustart die Sache nur
// schlimmer machen.
var ErrFFmpegDiedEarly = errors.New("ffmpeg beendet, bevor Playlist erstellt wurde")

func (s *Session) WaitForPlaylist(timeout time.Duration) error {
	playlist := filepath.Join(s.Dir, "index.m3u8")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(playlist); err == nil {
			return nil
		}
		select {
		case <-s.done:
			return ErrFFmpegDiedEarly
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("timeout beim Warten auf Playlist")
}

// ringBuffer haelt die letzten `max` Bytes eines Streams — fuer ffmpeg-stderr,
// das sonst unbegrenzt wachsen koennte. Schreibzugriffe kommen aus der
// exec-Goroutine, gelesen wird nach cmd.Wait(); der Mutex deckt den Fall ab,
// dass beides kurz ueberlappt.
type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimSpace(string(r.buf))
}

// sessionDirPattern matcht die Verzeichnisse, die StartOrGet anlegt
// (`<item>-<profil>-a<n>-<start>-d<0|1>-<suffix>`). Bewusst eng gefasst:
// im selben cacheDir liegen auch `downloads/` und `trailers/`, die NIEMALS
// angefasst werden duerfen — beide beginnen nicht mit `<ziffern>-` und koennen
// daher gar nicht matchen (abgesichert in session_dir_test.go).
//
// `-d<0|1>` und der Suffix sind OPTIONAL, weil beide erst nachtraeglich
// eingefuehrt wurden (der Suffix am 2026-09-13 gegen kollidierende
// ffmpeg-Schreibzugriffe). Aeltere Laeufe legten `<item>-<profil>-a<n>-<start>`
// bzw. `…-d0` an — ohne die optionalen Gruppen war der Aufraeumer fuer genau
// diese Altbestaende blind: am 2026-09-14 lagen 268 von 269 Verzeichnissen im
// alten Schema und damit rund 119 GB dauerhaft im Cache, die nie jemand
// geloescht haette.
var sessionDirPattern = regexp.MustCompile(`^\d+-.+-a-?\d+-\d+(-d[01])?(-[a-z0-9]+)?$`)

// cleanStaleSessionDirs entfernt Transcode-Verzeichnisse frueherer Laeufe.
// Noetig, seit jede Session einen eindeutigen Pfad bekommt: nach einem
// Container-Neustart wuerde sonst nichts mehr recycelt und der Cache waechst
// unbegrenzt. Laufende Sessions gibt es beim Start per Definition nicht.
func cleanStaleSessionDirs(cacheDir string) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() || !sessionDirPattern.MatchString(e.Name()) {
			continue
		}
		if os.RemoveAll(filepath.Join(cacheDir, e.Name())) == nil {
			n++
		}
	}
	if n > 0 {
		log.Printf("[transcode] %d verwaiste Session-Verzeichnisse aus einem frueheren Lauf entfernt", n)
	}
}
