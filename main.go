// main.go — точка входа.
//
// Что делает:
//   - читает конфиг (config.go);
//   - поднимает HTTP сервер: он нужен Koyeb как признак живого сервиса
//     и принимает вебхуки от Telegram на /webhook;
//   - передаёт каждое сообщение с файлом в batch.go.
//
// Зачем: связывает остальные части в один процесс.
package main

import (
	"booksender/internal"
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
)

func main() {
	if os.Getenv("LOG_LEVEL") == "debug" {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	slog.Info("Starting book sender service...")
	cfg, err := internal.LoadConfig()
	if err != nil {
		slog.Error("не смог загрузить конфиг", "error", err)
		os.Exit(1) // полная остановка, defer не выполняется
	}
	slog.Info("конфиг",
		"port", cfg.TelegramConfig.Port,
		"smtp_host", cfg.MailConfig.SMTPHost,
		"smtp_port", cfg.MailConfig.SMTPPort,
		"mail_to", cfg.MailConfig.MailTo,
		"batch_wait", cfg.MailConfig.BatchWait,
		"max_file_mb", cfg.MailConfig.MaxFileMB,
		"max_mail_mb", cfg.MailConfig.MaxMailMB,
		"max_batch_mb", cfg.MailConfig.MaxBatchMB,
		"tmp_dir", cfg.MailConfig.TmpDir,
	)

	// после перезапуска батч в памяти пуст, недосланные файлы уже не нужны
	if err := os.RemoveAll(cfg.MailConfig.TmpDir); err != nil {
		slog.Error("не смог очистить временную папку", "dir", cfg.MailConfig.TmpDir, "error", err)
	} else {
		slog.Info("временная папка очищена", "dir", cfg.MailConfig.TmpDir)
	}

	tg := internal.NewTelegram(cfg.TelegramConfig.TelegramToken)
	mailer := internal.NewMailer(cfg.MailConfig, tg)
	batch := internal.NewBatch(tg, mailer, cfg.MailConfig.BatchWait, cfg.MailConfig.MaxBatchMB, cfg.MailConfig.TmpDir)

	handlers(cfg, tg, batch)

	slog.Info("сервер слушает", "addr", ":"+cfg.TelegramConfig.Port)
	log.Fatal(http.ListenAndServe(":"+cfg.TelegramConfig.Port, nil))
}

func handlers(cfg *internal.Config, tg *internal.Telegram, batch *internal.Batch) {
	slog.Info("Setting up handlers...")

	maxFileBytes := int64(cfg.MailConfig.MaxFileMB) * 1024 * 1024

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok")) // ответ "ok" для Koyeb
	})

	http.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // не больше 1 МБ

		body, err := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK) // Telegram повторов не надо, отвечаем 200 всегда
		if err != nil {
			slog.Error("webhook: не прочитал тело", "error", err)
			return
		}

		book, ok := internal.ParseUpdate(body)
		if !ok {
			slog.Debug("webhook: сообщение без книги, пропущено")
			return
		}
		slog.Info("webhook: получена книга", "name", book.Name, "size", book.Size)

		if book.Size > maxFileBytes {
			slog.Warn("webhook: файл больше лимита, пропущен",
				"name", book.Name, "size_mb", book.Size/1024/1024, "limit_mb", cfg.MailConfig.MaxFileMB)
			text := fmt.Sprintf("Файл %s слишком большой (%d МБ), лимит %d МБ",
				book.Name, book.Size/1024/1024, cfg.MailConfig.MaxFileMB)
			go func() {
				if err := tg.SendMessage(context.Background(), book.ChatID, text); err != nil {
					slog.Error("webhook: не смог уведомить о большом файле", "error", err)
				} else {
					slog.Info("webhook: пользователь уведомлён о большом файле")
				}
			}()
			return
		}

		batch.Add(book)
	}) // приём сообщений от Telegram
}
