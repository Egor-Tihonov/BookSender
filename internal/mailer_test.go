package internal

import "testing"

func item(size int64) *Item { return &Item{Book: Book{Size: size}} }

func TestSplitByLimit(t *testing.T) {
	groups := splitByLimit([]*Item{item(5), item(6), item(20), item(3), item(4)}, 10)
	want := [][]int64{{5}, {6}, {20}, {3, 4}}
	if len(groups) != len(want) {
		t.Fatalf("групп %d, ожидали %d", len(groups), len(want))
	}
	for i, g := range groups {
		if len(g) != len(want[i]) {
			t.Fatalf("группа %d: %d книг, ожидали %d", i, len(g), len(want[i]))
		}
	}
}

func TestParseUpdate(t *testing.T) {
	body := []byte(`{"update_id":1,"message":{"message_id":7,"chat":{"id":-100},"document":{"file_id":"f","file_unique_id":"u","file_name":"Book.EPUB","file_size":123}}}`)
	b, ok := ParseUpdate(body)
	if !ok || b.UniqueID != "u" || b.ChatID != -100 || b.Size != 123 {
		t.Fatalf("неверный разбор: %+v ok=%v", b, ok)
	}
	if _, ok := ParseUpdate([]byte(`{"message":{"document":{"file_name":"x.pdf"}}}`)); ok {
		t.Fatal("pdf не должен проходить")
	}
	if _, ok := ParseUpdate([]byte(`{"message":{"text":"hi"}}`)); ok {
		t.Fatal("текст не должен проходить")
	}
}
