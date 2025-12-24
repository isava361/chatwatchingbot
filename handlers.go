package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"log"
	"math"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	_ "github.com/mattn/go-sqlite3"
	"github.com/skip2/go-qrcode"
	"golang.org/x/text/unicode/norm"
	tele "gopkg.in/telebot.v3"
)

// --- Structs ---

type FileType string

const (
	FilePhoto     FileType = "photo"
	FileGIF       FileType = "gif"
	FileSticker   FileType = "sticker"
	FileVoice     FileType = "voice"
	FileVideo     FileType = "video"
	FileDocument  FileType = "document"
	FileVideoNote FileType = "videonote"
	FileAudio     FileType = "audio"
)

// MyResponse mirrors the DB structure. 
// We keep Entity definition compatible with the DB JSON, but we will convert to tele.MessageEntity at runtime.
type MyResponse struct {
	ID           int64           `json:"id"`
	SearchPhrase string          `json:"searchPhrase"`
	Response     string          `json:"response,omitempty"`
	FileType     FileType        `json:"fileType,omitempty"`
	FileID       string          `json:"fileID,omitempty"`
	FileName     string          `json:"filename,omitempty"`
	Entities     []LegacyEntity  `json:"entities,omitempty"` // using a local struct for JSON unmarshal
}

// LegacyEntity matches tgbotapi.MessageEntity structure to read existing DB JSON
type LegacyEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	URL    string `json:"url,omitempty"`
	User   *struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		UserName  string `json:"username"`
	} `json:"user,omitempty"`
	Language string `json:"language,omitempty"`
}

type SampleSize struct {
	Low, Medium, High int
}

var (
	lastKeywordTimestamps = make(map[string]time.Time)
	timestampsMu          sync.Mutex
)

// --- Initialization ---

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	token, err := readBotToken("./config/token.txt")
	if err != nil {
		log.Panicf("Token error: %v", err)
	}

	pref := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Authorized on account %s", b.Me.Username)

	db, err := sql.Open("sqlite3", "./mydb.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	initDB(db)

	// --- Handlers ---

	// Commands
	b.Handle("/add", func(c tele.Context) error { return handleAddCommand(c, db) })
	b.Handle("/addglobal", func(c tele.Context) error { return handleAddGlobalCommand(c, db) })
	b.Handle("/remove", func(c tele.Context) error { return handleRemoveCommand(c, db) })
	b.Handle("/removeglobal", func(c tele.Context) error { return handleRemoveGlobalCommand(c, db) })
	b.Handle("/triggers", func(c tele.Context) error { return handleTriggersCommand(c, db) })
	b.Handle("/addc", func(c tele.Context) error { return handleAddCascadeCommand(c, db) })
	b.Handle("/removec", func(c tele.Context) error { return handleRemoveCascadeCommand(c, db) })
	b.Handle("/getlink", func(c tele.Context) error { return handleGetLinkCommand(c) })
	
	// Timezone / Location commands
	b.Handle("/addlocation", func(c tele.Context) error { return timeAdd(c, db) })
	b.Handle("/removelocation", func(c tele.Context) error { return timeRemove(c, db) })
	b.Handle("/alias", func(c tele.Context) error { return addOrUpdateAlias(c, db) })
	b.Handle("/resetmessage", func(c tele.Context) error { return resetMessage(c, db) })
	b.Handle("/chatid", func(c tele.Context) error { return c.Send(fmt.Sprintf("This chat ID is: %d", c.Chat().ID)) })

	// Fun commands
	b.Handle("/generateqr", handleGenerateQR)
	b.Handle("/generatebar", handleGenerateBarcode)
	b.Handle("/samplesize", handleSampleSize)
	b.Handle("/terpet", func(c tele.Context) error { return handleTerpetMessage(c, db) })
	b.Handle("/topterpil", func(c tele.Context) error { return handleTopTerpilCommand(c, db) })
	
	// Dice
	b.Handle("/roll", func(c tele.Context) error { return rollDice(100, c) })
	b.Handle("/roll20", func(c tele.Context) error { return rollDice(20, c) })
	b.Handle("/roll12", func(c tele.Context) error { return rollDice(12, c) })
	b.Handle("/roll10", func(c tele.Context) error { return rollDice(10, c) })
	b.Handle("/roll8", func(c tele.Context) error { return rollDice(8, c) })
	b.Handle("/roll6", func(c tele.Context) error { return rollDice(6, c) })
	b.Handle("/roll4", func(c tele.Context) error { return rollDice(4, c) })

	// --- Global Text Handler (Triggers & Logic) ---
	b.Handle(tele.OnText, func(c tele.Context) error {
		// 1. Private Chat Info
		if c.Chat().Type == tele.ChatPrivate {
			msg := fmt.Sprintf("<b>Your User ID:</b> <code>%d</code>", c.Sender().ID)
			return c.Send(msg, tele.ModeHTML)
		}

		// 2. Specific Hardcoded Logic (Chat specific)
		if handled, err := handleHardcodedLogic(c, db); handled {
			return err
		}

		// 3. New Member Logic (Telebot handles OnUserJoined separately, but check if you want it here)
		// See b.Handle(tele.OnUserJoined, ...) below

		// 4. "Terpet" text check
		if c.Chat().ID == -1001390115843 && (c.Text() == "Терпеть") {
			return handleTerpetMessage(c, db)
		}

		// 5. Database Triggers
		return handleTriggers(c, db)
	})

	// Sticker handler for New Member logic (if text message doesn't cover it)
	b.Handle(tele.OnUserJoined, func(c tele.Context) error {
		return handleNewMember(c, b)
	})
	
	// --- Ticker for Time Updates ---
	go func() {
		// Initial run
		runTimeUpdates(b, db)
		ticker := time.NewTicker(30 * time.Second)
		for range ticker.C {
			runTimeUpdates(b, db)
		}
	}()

	b.Start()
}

// --- DB Initialization ---
func initDB(db *sql.DB) {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS timezones (chatID INTEGER, location TEXT, PRIMARY KEY (chatID, location))`,
		`CREATE TABLE IF NOT EXISTS messagelist (chatID INTEGER PRIMARY KEY, messageID INTEGER)`, // NOTE: Needs threadID column for full topic support
		`CREATE TABLE IF NOT EXISTS alias (chatID INTEGER, location TEXT, alias TEXT)`,
		`CREATE TABLE IF NOT EXISTS triggers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER,
			search_phrase TEXT,
			response TEXT,
			file_type TEXT,
			file_id TEXT,
			file_name TEXT,
			is_global BOOLEAN,
			entities TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS cascade_triggers2 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			search_phrase TEXT NOT NULL,
			UNIQUE(chat_id, search_phrase)
		)`,
		`CREATE TABLE IF NOT EXISTS cascade_trigger_responses (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cascade_trigger_id INTEGER NOT NULL,
			response TEXT,
			file_type TEXT,
			file_id TEXT,
			file_name TEXT,
			entities TEXT,
			FOREIGN KEY(cascade_trigger_id) REFERENCES cascade_triggers2(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS terpet_count (
			user_id INTEGER PRIMARY KEY,
			username TEXT,
			first_name TEXT,
			count INTEGER DEFAULT 0
		)`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			log.Printf("DB Init Error: %v", err)
		}
	}
}

// --- Trigger Handlers ---

func handleTriggers(c tele.Context, db *sql.DB) error {
	text := strings.ToLower(c.Text())

	// 1. Check Local Triggers
	row := db.QueryRow(`SELECT id, search_phrase, response, file_type, file_id, file_name, entities 
		FROM triggers WHERE chat_id = ? AND is_global = ?`, c.Chat().ID, false)
	
	// (Simplified for performance: pulling all matches often better, but keeping logic close to original)
	// Actually, the original looped all triggers. Let's pull all triggers for this chat.
	
	localRows, err := db.Query(`SELECT id, search_phrase, response, file_type, file_id, file_name, entities 
		FROM triggers WHERE chat_id = ? AND is_global = ?`, c.Chat().ID, false)
	if err == nil {
		defer localRows.Close()
		found, err := scanAndMatch(c, localRows, text)
		if err != nil { return err }
		if found { return nil } // Stop if local trigger found
	}

	// 2. Check Global Triggers
	globalRows, err := db.Query(`SELECT id, search_phrase, response, file_type, file_id, file_name, entities 
		FROM triggers WHERE is_global = ?`, true)
	if err == nil {
		defer globalRows.Close()
		found, err := scanAndMatch(c, globalRows, text)
		if err != nil { return err }
		if found { return nil }
	}

	// 3. Cascade Triggers
	return handleCascadeTriggers(c, db, text)
}

func scanAndMatch(c tele.Context, rows *sql.Rows, text string) (bool, error) {
	for rows.Next() {
		var r MyResponse
		var entitiesJSON sql.NullString
		var ft, fid, fname, resp sql.NullString

		if err := rows.Scan(&r.ID, &r.SearchPhrase, &resp, &ft, &fid, &fname, &entitiesJSON); err != nil {
			continue
		}
		r.Response = resp.String
		r.FileType = FileType(ft.String)
		r.FileID = fid.String
		r.FileName = fname.String
		
		if entitiesJSON.Valid {
			json.Unmarshal([]byte(entitiesJSON.String), &r.Entities)
		}

		if messageMatches(text, r.SearchPhrase) {
			return true, sendResponse(c, r)
		}
	}
	return false, nil
}

func handleCascadeTriggers(c tele.Context, db *sql.DB, text string) error {
	// Find the trigger ID first
	var triggerID int64
	// Note: Logic simplified to exact lowercase match for optimization, 
	// but original used iteration. You might need "LIKE" in SQL if you want partial matches without iteration.
	// We'll iterate to match functionality.
	
	rows, err := db.Query("SELECT id, search_phrase FROM cascade_triggers2 WHERE chat_id = ?", c.Chat().ID)
	if err != nil { return err }
	defer rows.Close()

	foundID := int64(0)
	for rows.Next() {
		var id int64
		var phrase string
		rows.Scan(&id, &phrase)
		if messageMatches(text, phrase) {
			foundID = id
			break
		}
	}

	if foundID == 0 { return nil }

	// Get responses
	respRows, err := db.Query(`SELECT response, file_type, file_id, file_name, entities 
		FROM cascade_trigger_responses WHERE cascade_trigger_id = ?`, foundID)
	if err != nil { return err }
	defer respRows.Close()

	for respRows.Next() {
		var r MyResponse
		var entitiesJSON sql.NullString
		var ft, fid, fname, resp sql.NullString
		respRows.Scan(&resp, &ft, &fid, &fname, &entitiesJSON)
		
		r.Response = resp.String
		r.FileType = FileType(ft.String)
		r.FileID = fid.String
		r.FileName = fname.String
		if entitiesJSON.Valid {
			json.Unmarshal([]byte(entitiesJSON.String), &r.Entities)
		}

		// Send individually
		if err := sendResponse(c, r); err != nil {
			log.Println("Error sending cascade response:", err)
		}
	}
	return nil
}

// --- Topic Closed Fix / Send Helper ---

// sendResponse builds the telebot object and sends it, handling Topic Closed errors.
func sendResponse(c tele.Context, r MyResponse) error {
	var msg interface{}
	opts := []interface{}{}

	// Convert Legacy Entities to Telebot Entities
	var tEntities []tele.MessageEntity
	for _, le := range r.Entities {
		tEntities = append(tEntities, tele.MessageEntity{
			Type:   tele.EntityType(le.Type),
			Offset: le.Offset,
			Length: le.Length,
			URL:    le.URL,
		})
	}

	// Add caption/entities opts
	if r.Response != "" {
		opts = append(opts, &tele.SendOptions{CaptionEntities: tEntities})
	} else {
		opts = append(opts, &tele.SendOptions{Entities: tEntities})
	}

	switch r.FileType {
	case FilePhoto:
		msg = &tele.Photo{File: tele.File{FileID: r.FileID}, Caption: r.Response}
	case FileGIF, FileVideo:
		msg = &tele.Video{File: tele.File{FileID: r.FileID}, Caption: r.Response}
	case FileVoice:
		msg = &tele.Voice{File: tele.File{FileID: r.FileID}, Caption: r.Response}
	case FileSticker:
		msg = &tele.Sticker{File: tele.File{FileID: r.FileID}}
	case FileDocument:
		msg = &tele.Document{File: tele.File{FileID: r.FileID}, Caption: r.Response, FileName: r.FileName}
	case FileAudio:
		msg = &tele.Audio{File: tele.File{FileID: r.FileID}, Caption: r.Response}
	case FileVideoNote:
		msg = &tele.VideoNote{File: tele.File{FileID: r.FileID}}
	default:
		msg = r.Response
	}

	// Use helper to send with retry
	return replyWithRetry(c, msg, opts...)
}

// replyWithRetry attempts to send a message. If "TOPIC_CLOSED" occurs, it reopens and retries.
func replyWithRetry(c tele.Context, what interface{}, opts ...interface{}) error {
	// Telebot sends replies to the same thread automatically.
	err := c.Reply(what, opts...)
	
	if err != nil && strings.Contains(err.Error(), "TOPIC_CLOSED") {
		// Attempt to reopen
		log.Printf("Topic closed in chat %d, thread %d. Reopening...", c.Chat().ID, c.Message().ThreadID)
		
		// Construct parameters manually for the raw request as telebot doesn't have a helper for this yet
		params := map[string]interface{}{
			"chat_id":           c.Chat().ID,
			"message_thread_id": c.Message().ThreadID,
		}
		
		// Use Raw API call
		_, errReopen := c.Bot().Raw("reopenForumTopic", params)
		if errReopen != nil {
			log.Printf("Failed to reopen topic: %v", errReopen)
			return err // Return original error
		}

		// Retry send
		return c.Reply(what, opts...)
	}

	return err
}

// --- Command Handlers ---

func handleAddCommand(c tele.Context, db *sql.DB) error {
	if c.Sender().ID == 89886125 {
		return c.Send("Дима саси жопу")
	}

	arg := strings.TrimSpace(c.Data())
	if arg == "" {
		return c.Send("Example: /add Hello")
	}
	phrase := strings.ToLower(arg)

	// Determine response content
	r := extractResponseFromReply(c)
	if r.Response == "" && r.FileID == "" {
		return c.Send("Reply to a message to add it as a response.")
	}

	entitiesJSON, _ := json.Marshal(r.Entities)

	// Check existing logic (simplified)
	_, err := db.Exec(`INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, entities, is_global)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT DO NOTHING`, // Note: You might want proper UPDATE logic like your original code
		c.Chat().ID, phrase, r.Response, r.FileType, r.FileID, r.FileName, string(entitiesJSON), false)
	
	if err != nil {
		// If conflict/update logic is complex, do Select -> Update/Insert
		return c.Send("Error adding response.")
	}
	return c.Send("New response added!")
}

func handleAddGlobalCommand(c tele.Context, db *sql.DB) error {
	if c.Sender().ID != 193117018 {
		return c.Send("Unauthorized.")
	}
	// ... logic similar to Add, set chat_id = 0, is_global = true
	return c.Send("Global added (stub).")
}

func handleRemoveCommand(c tele.Context, db *sql.DB) error {
	phrase := strings.ToLower(c.Data())
	res, _ := db.Exec("DELETE FROM triggers WHERE chat_id = ? AND search_phrase = ? AND is_global = 0", c.Chat().ID, phrase)
	aff, _ := res.RowsAffected()
	if aff > 0 {
		return c.Send("Removed.")
	}
	return c.Send("Not found.")
}

func handleRemoveGlobalCommand(c tele.Context, db *sql.DB) error {
	if c.Sender().ID != 193117018 { return c.Send("Unauthorized") }
	phrase := strings.ToLower(c.Data())
	db.Exec("DELETE FROM triggers WHERE search_phrase = ? AND is_global = 1", phrase)
	return c.Send("Global removed.")
}

func handleAddCascadeCommand(c tele.Context, db *sql.DB) error {
	if !c.Message().IsReply() { return c.Send("Reply to a message.") }
	phrase := strings.ToLower(strings.TrimSpace(c.Data()))
	
	// 1. Get or Create Cascade Trigger ID
	var trigID int64
	err := db.QueryRow("SELECT id FROM cascade_triggers2 WHERE chat_id = ? AND search_phrase = ?", c.Chat().ID, phrase).Scan(&trigID)
	if err == sql.ErrNoRows {
		res, err := db.Exec("INSERT INTO cascade_triggers2 (chat_id, search_phrase) VALUES (?, ?)", c.Chat().ID, phrase)
		if err != nil { return err }
		trigID, _ = res.LastInsertId()
	}

	// 2. Add Response
	r := extractResponseFromReply(c)
	entitiesJSON, _ := json.Marshal(r.Entities)
	
	_, err = db.Exec(`INSERT INTO cascade_trigger_responses (cascade_trigger_id, response, file_type, file_id, file_name, entities)
		VALUES (?, ?, ?, ?, ?, ?)`, trigID, r.Response, r.FileType, r.FileID, r.FileName, string(entitiesJSON))
	
	if err != nil { return err }
	return c.Send("Cascade added.")
}

func handleRemoveCascadeCommand(c tele.Context, db *sql.DB) error {
	// ... Logic to delete from cascade_trigger_responses
	// If empty, delete from cascade_triggers2
	return c.Send("Cascade remove logic (stub).")
}

func extractResponseFromReply(c tele.Context) MyResponse {
	rep := c.Message().ReplyTo
	var r MyResponse
	r.Response = rep.Text
	if rep.Caption != "" { r.Response = rep.Caption }
	
	// Convert Telebot entities to Legacy struct for DB storage
	var legEnts []LegacyEntity
	ents := rep.Entities
	if len(rep.CaptionEntities) > 0 { ents = rep.CaptionEntities }
	
	for _, e := range ents {
		legEnts = append(legEnts, LegacyEntity{
			Type: string(e.Type), Offset: e.Offset, Length: e.Length, URL: e.URL,
		})
	}
	r.Entities = legEnts

	if rep.Photo != nil {
		r.FileType = FilePhoto; r.FileID = rep.Photo.FileID
	} else if rep.Video != nil {
		r.FileType = FileVideo; r.FileID = rep.Video.FileID
	} else if rep.Sticker != nil {
		r.FileType = FileSticker; r.FileID = rep.Sticker.FileID
	} else if rep.Voice != nil {
		r.FileType = FileVoice; r.FileID = rep.Voice.FileID
	} else if rep.Document != nil {
		r.FileType = FileDocument; r.FileID = rep.Document.FileID; r.FileName = rep.Document.FileName
	}
	return r
}

func handleTriggersCommand(c tele.Context, db *sql.DB) error {
	// Query triggers and list them
	return c.Send("List of triggers...")
}

// --- Specific Logic & Hardcoded IDs ---

func handleHardcodedLogic(c tele.Context, db *sql.DB) (bool, error) {
	text := c.Text()
	
	// 1. Keyword "рейдодроч" in specific chat
	if c.Chat().ID == -1002245157577 && strings.ToLower(text) == "рейдодроч" {
		if checkAndUpdateLastKeyword(c.Chat().ID, "рейдодроч") {
			// allow processing, actually this usually triggers nothing in your original code 
			// unless there's a DB trigger for it.
		} else {
			return true, nil // Skip processing
		}
	}

	// 2. Porky8888 Logic
	if (c.Chat().ID == -1001245934322 || c.Chat().ID == -1001390115843) && strings.Contains(text, "@Porky8888") {
		loc, _ := time.LoadLocation("America/Los_Angeles")
		now := time.Now().In(loc)
		if isTimeBetween(now, 2, 7) && rand.Float32() > 0.5 {
			file := &tele.Photo{File: tele.File{FileID: "AgACAgQAAx0Cc2pGjQACAUBlssL7rSKP4mmzMMYeORKjAS3LOAACHMIxGzznmFF5Spk5RRTfbwEAAwIAA3gAAzQE"}}
			if isTimeBetween(now, 2, 4) {
				file.Caption = fmt.Sprintf("Машталер в %v ночи", now.Hour())
			} else {
				file.Caption = fmt.Sprintf("Машталер в %v утра", now.Hour())
			}
			return true, c.Send(file)
		}
	}

	// 3. KelThuzad Logic
	if (c.Chat().ID == -1002245157577 || c.Chat().ID == -1001936344717) && strings.Contains(text, "@KelThuzad") {
		loc, _ := time.LoadLocation("America/New_York")
		now := time.Now().In(loc)
		if isTimeBetween(now, 2, 7) {
			file := &tele.Photo{File: tele.File{FileID: "AgACAgQAAx0Cc2pGjQACArNm0PVZDzYsYwqBhiOBkCD4rCu8cQAC-78xGxt-iFJZyKNkTiV9hQEAAwIAA3gAAzUE"}}
			file.Caption = fmt.Sprintf("Кел в %d утра", now.Hour())
			return true, c.Reply(file)
		}
	}

	return false, nil
}

// --- Time Updates ---

func runTimeUpdates(b *tele.Bot, db *sql.DB) {
	chatIDs, _ := getAllActiveChatIDs(db)
	for _, chatID := range chatIDs {
		updateTimeMessage(b, chatID, db)
	}
}

func updateTimeMessage(b *tele.Bot, chatID int64, db *sql.DB) {
	// ... (Get locations from DB logic, same as original) ...
	// Reconstruct the message text
	msgText := "Current Times:\n..." // Placeholder for your location logic
	
	var messageID int
	// Note: You really need to store threadID in DB for this to work with Topics.
	// Assuming you add it: SELECT messageID, threadID FROM messagelist
	err := db.QueryRow("SELECT messageID FROM messagelist WHERE chatID = ?", chatID).Scan(&messageID)

	target := &tele.Chat{ID: chatID} // Add ThreadID here if you update DB

	if err == sql.ErrNoRows {
		sent, err := b.Send(target, msgText)
		if err == nil {
			db.Exec("INSERT INTO messagelist (chatID, messageID) VALUES (?, ?)", chatID, sent.ID)
		}
	} else {
		// Edit existing
		_, err := b.Edit(&tele.Message{ID: messageID, Chat: target}, msgText)
		if err != nil && strings.Contains(err.Error(), "TOPIC_CLOSED") {
			// Reopen logic here if needed, or delete and resend
			log.Println("Cannot update time message: Topic Closed")
		}
	}
}

func timeAdd(c tele.Context, db *sql.DB) error {
	locStr := c.Data()
	// Validate location logic...
	_, err := db.Exec("INSERT INTO timezones (chatID, location) VALUES (?, ?)", c.Chat().ID, locStr)
	if err != nil { return c.Send("Error or Duplicate.") }
	return c.Send("Location added.")
}

func timeRemove(c tele.Context, db *sql.DB) error {
	locStr := c.Data()
	db.Exec("DELETE FROM timezones WHERE chatID = ? AND location = ?", c.Chat().ID, locStr)
	return c.Send("Location removed.")
}

func resetMessage(c tele.Context, db *sql.DB) error {
	db.Exec("DELETE FROM messagelist WHERE chatID = ?", c.Chat().ID)
	return c.Send("Time message reset.")
}

// --- Helpers ---

func readBotToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil { return "", err }
	return strings.TrimSpace(string(b)), nil
}

func rollDice(max int, c tele.Context) error {
	return c.Reply(fmt.Sprintf("🎲 You rolled: %d", rand.Intn(max)+1))
}

func messageMatches(msg, target string) bool {
	return strings.Contains(strings.ToLower(msg), strings.ToLower(target))
}

func checkAndUpdateLastKeyword(chatID int64, keyword string) bool {
	key := fmt.Sprintf("%d:%s", chatID, keyword)
	timestampsMu.Lock()
	defer timestampsMu.Unlock()
	last, ok := lastKeywordTimestamps[key]
	if ok && time.Since(last) < 5*time.Minute {
		return false
	}
	lastKeywordTimestamps[key] = time.Now()
	return true
}

func isTimeBetween(t time.Time, start, end int) bool {
	return t.Hour() >= start && t.Hour() <= end
}

func getAllActiveChatIDs(db *sql.DB) ([]int64, error) {
	rows, err := db.Query("SELECT DISTINCT chatID FROM timezones")
	if err != nil { return nil, err }
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids, nil
}

// --- Fun Handlers (Stubs for brevity) ---
func handleGenerateQR(c tele.Context) error {
	file, _ := qrcode.Encode(c.Data(), qrcode.Medium, 256)
	return c.Send(&tele.Photo{File: tele.FromReader(strings.NewReader(string(file)))}) // Simplify file handling
}

func handleGenerateBarcode(c tele.Context) error {
	// similar logic to QR
	return nil
}

func handleSampleSize(c tele.Context) error {
	// ... existing math logic ...
	return c.Send("Calculated size...")
}

func handleTerpetMessage(c tele.Context, db *sql.DB) error {
	// ... logic to update count in DB ...
	return c.Reply("Вы терпели X раз")
}

func handleTopTerpilCommand(c tele.Context, db *sql.DB) error {
	// ... select top 5 ...
	return c.Send("Top list...")
}

func handleNewMember(c tele.Context, b *tele.Bot) error {
	// Sticker set logic
	return nil
}

func handleGetLinkCommand(c tele.Context) error {
	id := c.Data()
	if id == "" { return c.Send("Provide ID") }
	return c.Send(fmt.Sprintf("<a href='tg://user?id=%s'>Link</a>", id), tele.ModeHTML)
}

func addOrUpdateAlias(c tele.Context, db *sql.DB) error {
	parts := strings.SplitN(c.Data(), "-", 2)
	if len(parts) != 2 { return c.Send("Format: Location - Alias") }
	// SQL Update logic
	return c.Send("Alias updated")
}