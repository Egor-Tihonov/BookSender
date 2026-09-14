// telegram.go — обёртка над Telegram Bot API.
//
// Что делает: пять вызовов через net/http и encoding/json:
//   getUpdates   опрос сообщений (режим polling)
//   setWebhook   регистрация адреса вебхука (режим webhook)
//   getFile      получить путь к файлу по file_id
//   download     скачать файл по пути во временную папку
//   sendMessage  уведомить чат: файл слишком большой, ошибка отправки
//
// Здесь же фильтр входящего сообщения: есть ли документ,
// расширение epub/mobi/fb2, размер не больше MAX_FILE_MB.
//
// Зачем: остальной код не знает про HTTP и JSON Telegram,
// он работает с простыми структурами Book{FileID, UniqueID, Name, Size}.
package main
