// config.go — конфигурация из переменных окружения.
//
// Что делает: читает и проверяет переменные:
//
//	TELEGRAM_TOKEN   токен бота
//	MODE             webhook или polling
//	WEBHOOK_URL      публичный адрес сервиса на Koyeb (для webhook)
//	PORT             порт HTTP сервера (Koyeb передаёт сам)
//	SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASS  доступ к почте
//	MAIL_TO          адрес получателя книг
//	BATCH_WAIT       окно батча, по умолчанию 5m
//	MAX_FILE_MB      лимит одного файла, по умолчанию 20
//	MAX_MAIL_MB      лимит книг в одном письме, по умолчанию 18
//
// Зачем: секреты и настройки не лежат в коде, их меняют
// в панели Koyeb или в .env локально без пересборки.
package internal

import (
	"log/slog"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	TelegramToken string        `yaml:"TELEGRAM_TOKEN" env:"TELEGRAM_TOKEN"`
	Mode          string        `yaml:"MODE" env:"MODE"`
	WebhookURL    string        `yaml:"WEBHOOK_URL" env:"WEBHOOK_URL"`
	Port          string        `yaml:"PORT" env:"PORT"`
	SMTPHost      string        `yaml:"SMTP_HOST" env:"SMTP_HOST"`
	SMTPPort      string        `yaml:"SMTP_PORT" env:"SMTP_PORT"`
	SMTPUser      string        `yaml:"SMTP_USER" env:"SMTP_USER"`
	SMTPPass      string        `yaml:"SMTP_PASS" env:"SMTP_PASS"`
	SMTPFrom      string        `yaml:"SMTP_FROM" env:"SMTP_FROM"`
	MaxFileMB     int           `yaml:"MAX_FILE_MB" env:"MAX_FILE_MB"`
	MaxMailMB     int           `yaml:"MAX_MAIL_MB" env:"MAX_MAIL_MB"`
	BatchWait     time.Duration `yaml:"BATCH_WAIT" env:"BATCH_WAIT"`
	MailTo        string        `yaml:"MAIL_TO" env:"MAIL_TO"`
}

func LoadConfig() (*Config, error) {
	cfg := &Config{}
	err := cleanenv.ReadEnv(cfg)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return nil, err
	}
	return cfg, nil
}
