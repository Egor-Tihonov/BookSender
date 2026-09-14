// main.go — точка входа.
//
// Что делает:
//   - читает конфиг (config.go);
//   - поднимает HTTP сервер: он нужен Koyeb как признак живого сервиса
//     и принимает вебхуки от Telegram на /webhook;
//   - в режиме MODE=polling (локальная разработка) вместо вебхука
//     опрашивает Telegram через getUpdates;
//   - передаёт каждое сообщение с файлом в batch.go.
//
// Зачем: связывает остальные части в один процесс и решает,
// как бот получает сообщения: вебхук на сервере или опрос на ПК.
package main

import (
	"booksender/internal"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
)

func main() {
	slog.Info("Starting book sender service...")
	_, err := internal.LoadConfig()
	if err != nil {
		os.Exit(1) // полная остановка, defer не выполняется
	}

	handlers()
	slog.Info("Starting server...")
	log.Fatal(http.ListenAndServe(":8000", nil))
}

func handlers() {
	slog.Info("Setting up handlers...")
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Println("ok") }) // ответ "ok" для Koyeb
	http.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // не больше 1 МБ

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("webhook: не прочитал тело: %v", err)
			w.WriteHeader(http.StatusOK) // Telegram повторов не надо
			return
		}

		fmt.Println("get body", string(body))

		w.WriteHeader(http.StatusOK)
	}) // приём сообщений от Telegram
}
