// mailer.go — отправка книг на почту.
//
// Что делает:
//   - делит батч на письма так, чтобы книг в одном письме
//     было не больше MAX_MAIL_MB (18 МБ, с base64 это около 25 МБ);
//   - собирает MIME письмо с вложениями: mime/multipart, encoding/base64;
//   - шлёт через net/smtp на порт 465 или 587 с таймаутом 30 секунд;
//   - при ошибке повторяет до 3 раз с паузой 10 секунд, после провала
//     сообщает в чат через sendMessage;
//   - удаляет файлы только отправленных книг, провалившиеся остаются для повтора.
//
// Зачем: изолирует всё, что связано с почтой. Сменить SMTP
// на Gmail или Яндекс можно одними переменными окружения.
package internal

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"strings"
	"time"
)

// Mailer шлёт книги почтой и уведомляет о провалах в Telegram.
type Mailer struct {
	cfg MailConfig
	tg  *Telegram
}

// NewMailer создаёт отправителя писем.
func NewMailer(cfg MailConfig, tg *Telegram) *Mailer {
	return &Mailer{cfg: cfg, tg: tg}
}

// Send делит батч на письма, шлёт их и возвращает успешно отправленные items.
func (m *Mailer) Send(ctx context.Context, items []*Item) []*Item {
	var ok, failed []*Item
	for _, it := range items {
		if it.Err != nil || it.Path == "" {
			failed = append(failed, it) // не скачался
			continue
		}
		ok = append(ok, it)
	}
	slog.Info("mailer: начало отправки", "total", len(items), "downloaded", len(ok), "not_downloaded", len(failed))

	maxMailBytes := int64(m.cfg.MaxMailMB) * 1024 * 1024
	groups := splitByLimit(ok, maxMailBytes)

	var sent []*Item
	for i, group := range groups {
		var groupSize int64
		for _, it := range group {
			groupSize += it.Book.Size
		}
		slog.Info("mailer: письмо", "index", i+1, "of", len(groups), "files", len(group), "size_mb", groupSize/1024/1024)

		var lastErr error
		success := false
		for attempt := 1; attempt <= 3; attempt++ {
			lastErr = m.sendOne(group)
			if lastErr == nil {
				success = true
				break
			}
			slog.Error("mailer: попытка отправки не удалась", "attempt", attempt, "error", lastErr)
			if attempt < 3 {
				time.Sleep(10 * time.Second)
			}
		}
		if success {
			sent = append(sent, group...)
			slog.Info("mailer: письмо отправлено", "index", i+1, "files", len(group))
		} else {
			slog.Error("mailer: не отправил письмо после 3 попыток", "error", lastErr)
			failed = append(failed, group...)
		}
	}

	if len(failed) > 0 {
		names := make([]string, 0, len(failed))
		for _, it := range failed {
			names = append(names, it.Book.Name)
		}
		text := fmt.Sprintf("Не удалось отправить: %s", strings.Join(names, ", "))
		if err := m.tg.SendMessage(ctx, failed[0].Book.ChatID, text); err != nil {
			slog.Error("mailer: не смог уведомить о провале", "error", err)
		} else {
			slog.Info("mailer: пользователь уведомлён о провале", "count", len(failed))
		}
	}

	// удаляем только то, что реально ушло; файлы провалившихся остаются для повтора
	for _, it := range sent {
		if err := os.Remove(it.Path); err != nil {
			slog.Error("mailer: не удалил временный файл", "path", it.Path, "error", err)
		} else {
			slog.Debug("mailer: временный файл удалён", "path", it.Path)
		}
	}

	return sent
}

// splitByLimit жадно набирает письма пока сумма Size не превышает limit.
// Книга больше limit идёт отдельным письмом одна.
func splitByLimit(items []*Item, limit int64) [][]*Item {
	var groups [][]*Item
	var cur []*Item
	var curSize int64

	flush := func() {
		if len(cur) > 0 {
			groups = append(groups, cur)
			cur = nil
			curSize = 0
		}
	}

	for _, it := range items {
		size := it.Book.Size
		if size > limit {
			flush()
			groups = append(groups, []*Item{it})
			continue
		}
		if curSize+size > limit {
			flush()
		}
		cur = append(cur, it)
		curSize += size
	}
	flush()

	return groups
}

// isASCII проверяет, что строка состоит только из ASCII-символов.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// buildMessage собирает MIME письмо со списком книг во вложениях.
func (m *Mailer) buildMessage(items []*Item) ([]byte, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	var names strings.Builder
	for _, it := range items {
		names.WriteString(it.Book.Name)
		names.WriteString("\n")
	}

	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	tw, err := mw.CreatePart(textHeader)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(tw, "Книги во вложении:\n%s", names.String())

	for _, it := range items {
		data, err := os.ReadFile(it.Path)
		if err != nil {
			return nil, err
		}

		filename := it.Book.Name
		if !isASCII(filename) {
			filename = mime.QEncoding.Encode("utf-8", filename)
		}

		ah := textproto.MIMEHeader{}
		ah.Set("Content-Type", "application/octet-stream")
		ah.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		ah.Set("Content-Transfer-Encoding", "base64")
		pw, err := mw.CreatePart(ah)
		if err != nil {
			return nil, err
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			pw.Write([]byte(encoded[i:end]))
			pw.Write([]byte("\r\n"))
		}
	}

	if err := mw.Close(); err != nil {
		return nil, err
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", m.cfg.SMTPFrom)
	fmt.Fprintf(&msg, "To: %s\r\n", m.cfg.MailTo)
	fmt.Fprintf(&msg, "Subject: Books: %d файлов\r\n", len(items))
	msg.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/mixed; boundary=%s\r\n", mw.Boundary())
	msg.WriteString("\r\n")
	msg.Write(body.Bytes())

	return msg.Bytes(), nil
}

// sendOne отправляет одно письмо через SMTP.
func (m *Mailer) sendOne(items []*Item) error {
	host := m.cfg.SMTPHost
	port := m.cfg.SMTPPort
	addr := net.JoinHostPort(host, port)

	var conn net.Conn
	var err error
	if port == "465" {
		dialer := &net.Dialer{Timeout: 30 * time.Second}
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = net.DialTimeout("tcp", addr, 30*time.Second)
	}
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(3 * time.Minute))

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	if port != "465" {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}

	auth := smtp.PlainAuth("", m.cfg.SMTPUser, m.cfg.SMTPPass, host)
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if err := c.Mail(m.cfg.SMTPFrom); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := c.Rcpt(m.cfg.MailTo); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}

	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}

	msg, err := m.buildMessage(items)
	if err != nil {
		wc.Close()
		return fmt.Errorf("build message: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	if err := c.Quit(); err != nil {
		return fmt.Errorf("quit: %w", err)
	}
	return nil
}
