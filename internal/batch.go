// batch.go — накопление книг и таймер батча.
//
// Что делает:
//   - при получении книги сразу начинает скачивание файла параллельно
//     с приёмом новых вебхуков;
//   - помнит unique_id уже отправленных книг, чтобы не слать дубли;
//   - при первой книге запускает таймер BATCH_WAIT (5 минут),
//     новые книги таймер не сбрасывают;
//   - если суммарный вес батча превышает MAX_BATCH_MB, текущий батч
//     уходит сразу, не дожидаясь таймера;
//   - по таймеру (или переполнению) отдаёт список в mailer.go (Sender)
//     и очищает батч;
//   - книги, которые не ушли почтой, возвращаются в батч и уходят
//     со следующей отправкой, максимум 3 круга, потом файл удаляется.
//
// Доступ к состоянию защищён sync.Mutex: вебхуки приходят параллельно.
//
// Зачем: книги уходят одним письмом, а не по одной.
package internal

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Item — книга батча: сообщение + результат скачивания.
type Item struct {
	Book  Book
	Path  string // путь на диске после скачивания
	Err   error  // ошибка скачивания
	Tries int    // сколько раз книга уже уходила в отправку
}

// maxTries — сколько раз батч пробует отправить книгу, прежде чем сдаться.
const maxTries = 3

// Sender отправляет книги батча (почта). Возвращает успешно отправленные.
type Sender interface {
	Send(ctx context.Context, items []*Item) []*Item
}

// Batch копит книги и отправляет их одним куском.
type Batch struct {
	mu       sync.Mutex
	pending  map[string]*Item // ключ UniqueID — «снимок» файла
	order    []string         // порядок добавления UniqueID
	total    int64            // сумма Size текущего батча
	wg       *sync.WaitGroup  // скачивания текущего батча
	timer    *time.Timer
	sent     map[string]struct{} // UniqueID уже отправленных
	wait     time.Duration
	maxBatch int64 // байты
	tmpDir   string
	tg       *Telegram
	sender   Sender
}

// NewBatch создаёт пустой батч.
func NewBatch(tg *Telegram, sender Sender, wait time.Duration, maxBatchMB int, tmpDir string) *Batch {
	return &Batch{
		pending:  make(map[string]*Item),
		sent:     make(map[string]struct{}),
		wait:     wait,
		maxBatch: int64(maxBatchMB) * 1024 * 1024,
		tmpDir:   tmpDir,
		tg:       tg,
		sender:   sender,
	}
}

// Add добавляет книгу в батч и запускает её скачивание.
func (b *Batch) Add(book Book) {
	b.mu.Lock()

	if _, ok := b.sent[book.UniqueID]; ok {
		b.mu.Unlock()
		slog.Info("batch: дубль книги, уже отправлена", "name", book.Name)
		return
	}
	if _, ok := b.pending[book.UniqueID]; ok {
		b.mu.Unlock()
		slog.Info("batch: дубль книги, уже в батче", "name", book.Name)
		return
	}

	if len(b.pending) > 0 && b.total+book.Size > b.maxBatch {
		slog.Warn("batch: батч переполнен, отправляем досрочно",
			"total_mb", b.total/1024/1024, "max_mb", b.maxBatch/1024/1024)
		b.flushLocked() // текущий батч переполнен — отправить сразу
	}

	item := &Item{Book: book}
	b.pending[book.UniqueID] = item
	b.order = append(b.order, book.UniqueID)
	b.total += book.Size

	if b.wg == nil {
		b.wg = new(sync.WaitGroup)
	}
	b.wg.Add(1)
	go b.download(item, b.wg)

	if b.timer == nil {
		b.timer = time.AfterFunc(b.wait, b.flush)
		slog.Info("batch: таймер запущен", "wait", b.wait)
	}

	slog.Info("batch: книга добавлена",
		"name", book.Name, "size", book.Size, "count", len(b.pending), "total_mb", b.total/1024/1024)

	b.mu.Unlock()
}

// download качает файл книги; выполняется в отдельной горутине.
func (b *Batch) download(item *Item, wg *sync.WaitGroup) {
	defer wg.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	path, err := b.tg.Download(ctx, item.Book.FileID, b.tmpDir)

	b.mu.Lock()
	item.Path = path
	item.Err = err
	b.mu.Unlock()

	if err != nil {
		slog.Error("batch: не скачал книгу", "name", item.Book.Name, "error", err)
		return
	}
	slog.Info("batch: книга скачана", "name", item.Book.Name, "path", path)
}

// flush — сработка таймера батча.
func (b *Batch) flush() {
	b.mu.Lock()
	b.flushLocked()
	b.mu.Unlock()
}

// flushLocked забирает текущий батч и запускает отправку. Вызывается под b.mu.
func (b *Batch) flushLocked() {
	if len(b.pending) == 0 {
		return
	}

	items := make([]*Item, 0, len(b.order))
	for _, id := range b.order {
		items = append(items, b.pending[id])
	}
	wg := b.wg

	b.pending = make(map[string]*Item)
	b.order = nil
	b.total = 0
	b.wg = nil
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}

	slog.Info("batch: батч закрыт, ждём скачивания", "count", len(items))

	go func() {
		wg.Wait() // дождаться всех скачиваний батча
		slog.Info("batch: все скачивания завершены, отправка", "count", len(items))

		sentItems := b.sender.Send(context.Background(), items)
		slog.Info("batch: итог отправки", "sent", len(sentItems), "failed", len(items)-len(sentItems))

		sentSet := make(map[string]struct{}, len(sentItems))
		for _, it := range sentItems {
			sentSet[it.Book.UniqueID] = struct{}{}
		}

		b.mu.Lock()
		defer b.mu.Unlock()
		for _, it := range sentItems {
			b.sent[it.Book.UniqueID] = struct{}{}
		}
		for _, it := range items {
			if _, ok := sentSet[it.Book.UniqueID]; ok {
				continue
			}
			b.requeueLocked(it)
		}
	}()
}

// requeueLocked возвращает неотправленную книгу в батч для повтора. Вызывается под b.mu.
func (b *Batch) requeueLocked(it *Item) {
	if it.Path == "" {
		return // не скачалась — повторять нечего, ошибка уже в логе и в чате
	}
	it.Tries++
	if it.Tries >= maxTries {
		slog.Error("batch: книга не ушла после всех попыток, удаляю файл", "name", it.Book.Name)
		if err := os.Remove(it.Path); err != nil {
			slog.Error("batch: не удалил файл", "path", it.Path, "error", err)
		}
		return
	}
	if _, ok := b.pending[it.Book.UniqueID]; ok {
		return
	}
	b.pending[it.Book.UniqueID] = it
	b.order = append(b.order, it.Book.UniqueID)
	b.total += it.Book.Size
	if b.wg == nil {
		b.wg = new(sync.WaitGroup) // скачивать не нужно, файл уже на диске
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(b.wait, b.flush) // повтор через то же окно
	}
	slog.Info("batch: книга вернулась в батч для повтора", "name", it.Book.Name, "try", it.Tries)
}
