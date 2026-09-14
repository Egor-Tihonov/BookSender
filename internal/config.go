// config.go — конфигурация из переменных окружения.
//
// Что делает: читает и проверяет переменные:
//
//	TELEGRAM_TOKEN   токен бота
//	WEBHOOK_URL      публичный адрес сервиса на Koyeb (для вебхука)
//	PORT             порт HTTP сервера, по умолчанию 8000
//	SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASS  доступ к почте
//	MAIL_TO          адрес получателя книг
//	BATCH_WAIT       окно батча, по умолчанию 5m
//	MAX_FILE_MB      лимит одного файла, по умолчанию 20
//	MAX_MAIL_MB      лимит книг в одном письме, по умолчанию 18
//	MAX_BATCH_MB     общий лимит книг в одном батче, по умолчанию 50
//	TMP_DIR          папка для скачанных книг, по умолчанию tmp
//
// Зачем: секреты и настройки не лежат в коде, их меняют
// в панели Koyeb или в .env локально без пересборки.
package internal

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	TelegramConfig TelegramConfig `yaml:"TELEGRAM"`
	MailConfig     MailConfig     `yaml:"MAIL"`
}

type TelegramConfig struct {
	TelegramToken string `yaml:"TELEGRAM_TOKEN" env:"TELEGRAM_TOKEN"`
	WebhookURL    string `yaml:"WEBHOOK_URL" env:"WEBHOOK_URL"`
	Port          string `yaml:"PORT" env:"PORT" env-default:"8000"`
}

type MailConfig struct {
	SMTPHost   string        `yaml:"SMTP_HOST" env:"SMTP_HOST"`
	SMTPPort   string        `yaml:"SMTP_PORT" env:"SMTP_PORT" env-default:"587"`
	SMTPUser   string        `yaml:"SMTP_USER" env:"SMTP_USER"`
	SMTPPass   string        `yaml:"SMTP_PASS" env:"SMTP_PASS"`
	SMTPFrom   string        `yaml:"SMTP_FROM" env:"SMTP_FROM"`
	MaxFileMB  int           `yaml:"MAX_FILE_MB" env:"MAX_FILE_MB" env-default:"20"`
	MaxMailMB  int           `yaml:"MAX_MAIL_MB" env:"MAX_MAIL_MB" env-default:"18"`
	MaxBatchMB int           `yaml:"MAX_BATCH_MB" env:"MAX_BATCH_MB" env-default:"50"`
	BatchWait  time.Duration `yaml:"BATCH_WAIT" env:"BATCH_WAIT" env-default:"5m"`
	MailTo     string        `yaml:"MAIL_TO" env:"MAIL_TO"`
	TmpDir     string        `yaml:"TMP_DIR" env:"TMP_DIR" env-default:"tmp"`
}

func LoadConfig() (*Config, error) {
	slog.Info("Loading config...")
	cfg := &Config{}
	// локально переменные берём из .env, на сервере файла нет и читается окружение
	var err error
	if _, statErr := os.Stat(".env"); statErr == nil {
		slog.Info("конфиг читается из .env")
		err = cleanenv.ReadConfig(".env", cfg)
	} else {
		slog.Info("конфиг читается из окружения")
		err = cleanenv.ReadEnv(cfg)
	}
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return nil, err
	}

	// проверка обязательных переменных, порядок фиксированный
	required := []struct{ name, val string }{
		{"TELEGRAM_TOKEN", cfg.TelegramConfig.TelegramToken},
		{"SMTP_HOST", cfg.MailConfig.SMTPHost},
		{"SMTP_USER", cfg.MailConfig.SMTPUser},
		{"SMTP_PASS", cfg.MailConfig.SMTPPass},
		{"MAIL_TO", cfg.MailConfig.MailTo},
	}
	for _, r := range required {
		name, val := r.name, r.val
		if val == "" {
			err := fmt.Errorf("не задана обязательная переменная %s", name)
			slog.Error("failed to load config", "error", err)
			return nil, err
		}
	}

	if cfg.MailConfig.SMTPFrom == "" {
		cfg.MailConfig.SMTPFrom = cfg.MailConfig.SMTPUser
	}

	slog.Info("Config loaded successfully")
	return cfg, nil
}
