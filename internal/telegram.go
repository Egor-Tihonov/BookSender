// telegram.go — обёртка над Telegram Bot API.
//
// Что делает: три вызова через net/http и encoding/json:
//
//	getFile      получить путь к файлу по file_id
//	download     скачать файл по пути во временную папку
//	sendMessage  уведомить чат: файл слишком большой, ошибка отправки
//
// Здесь же разбор входящего вебхука: есть ли документ,
// расширение epub/mobi/fb2. Размер файла тут не проверяется —
// это делает вызывающий код (main.go).
//
// Зачем: остальной код не знает про HTTP и JSON Telegram,
// он работает с простыми структурами Book{FileID, UniqueID, Name, Size}.
package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Book — файл книги из сообщения Telegram.
type Book struct {
	FileID    string
	UniqueID  string // file_unique_id, ключ дедупликации
	Name      string
	Size      int64 // байты
	ChatID    int64
	MessageID int64
}

// Telegram — клиент Bot API.
type Telegram struct {
	token  string
	client *http.Client
}

// NewTelegram создаёт клиента с токеном бота.
func NewTelegram(token string) *Telegram {
	return &Telegram{
		token:  token,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Update — минимальный набор полей вебхука Telegram.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// Message — сообщение с документом.
type Message struct {
	MessageID int64 `json:"message_id"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Document *Document `json:"document"`
}

// Document — файл, приложенный к сообщению.
type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	FileSize     int64  `json:"file_size"`
}

// разрешённые расширения книг
var allowedExt = map[string]bool{
	".epub": true,
	".fb2":  true,
	".mobi": true,
}

// ParseUpdate разбирает тело вебхука в Book.
// Возвращает false, если это не документ книги.
func ParseUpdate(body []byte) (Book, bool) {
	var upd Update
	if err := json.Unmarshal(body, &upd); err != nil {
		return Book{}, false
	}
	if upd.Message == nil || upd.Message.Document == nil {
		return Book{}, false
	}
	doc := upd.Message.Document
	ext := strings.ToLower(filepath.Ext(doc.FileName))
	if !allowedExt[ext] {
		return Book{}, false
	}
	return Book{
		FileID:    doc.FileID,
		UniqueID:  doc.FileUniqueID,
		Name:      doc.FileName,
		Size:      doc.FileSize,
		ChatID:    upd.Message.Chat.ID,
		MessageID: upd.Message.MessageID,
	}, true
}

// getFileResponse — ответ метода getFile.
type getFileResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		FilePath string `json:"file_path"`
	} `json:"result"`
}

// Download скачивает файл fileID в папку dir. Возвращает путь на диске.
func (t *Telegram) Download(ctx context.Context, fileID, dir string) (string, error) {
	slog.Info("telegram: скачивание начато", "file_id", fileID)

	getFileURL := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", t.token, fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getFileURL, nil)
	if err != nil {
		slog.Error("telegram: getFile не удался", "file_id", fileID, "error", err)
		return "", err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		slog.Error("telegram: getFile не удался", "file_id", fileID, "error", err)
		return "", err
	}
	defer resp.Body.Close()

	var gf getFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&gf); err != nil {
		slog.Error("telegram: getFile не удался", "file_id", fileID, "error", err)
		return "", err
	}
	if !gf.OK {
		err := fmt.Errorf("getFile: %s", gf.Description)
		slog.Error("telegram: getFile не удался", "file_id", fileID, "error", err)
		return "", err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	fileURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", t.token, gf.Result.FilePath)
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return "", err
	}
	dlResp, err := t.client.Do(dlReq)
	if err != nil {
		return "", err
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		slog.Error("telegram: скачивание отклонено", "status", dlResp.StatusCode)
		return "", fmt.Errorf("download: статус %d", dlResp.StatusCode)
	}

	path := filepath.Join(dir, filepath.Base(gf.Result.FilePath))
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()

	size, err := io.Copy(out, dlResp.Body)
	if err != nil {
		slog.Error("telegram: скачивание прервано", "path", path, "error", err)
		out.Close()
		os.Remove(path) // не оставлять недокачанный файл
		return "", err
	}

	slog.Info("telegram: файл скачан", "path", path, "size_bytes", size)
	return path, nil
}

// SendMessage шлёт текстовое сообщение в чат.
func (t *Telegram) SendMessage(ctx context.Context, chatID int64, text string) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("telegram: sendMessage не удался", "chat_id", chatID, "status", resp.StatusCode)
		return fmt.Errorf("sendMessage: статус %d", resp.StatusCode)
	}
	slog.Info("telegram: сообщение отправлено в чат", "chat_id", chatID)
	return nil
}
