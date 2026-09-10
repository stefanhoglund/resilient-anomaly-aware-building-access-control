package eventbus

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestSSE_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSSEEvent(&buf, "01ABC", "badge.read", []byte(`{"a":1}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := writeSSEComment(&buf, "ping"); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if err := writeSSEEvent(&buf, "01ABD", "door.state", []byte("line1\nline2")); err != nil {
		t.Fatalf("write multiline: %v", err)
	}

	dec := newSSEDecoder(&buf)

	ev, err := dec.next()
	if err != nil {
		t.Fatalf("next 1: %v", err)
	}
	if ev.id != "01ABC" || ev.event != "badge.read" || string(ev.data) != `{"a":1}` {
		t.Fatalf("event 1 = %+v", ev)
	}

	ev, err = dec.next()
	if err != nil {
		t.Fatalf("next 2: %v", err)
	}
	if len(ev.data) != 0 {
		t.Fatalf("expected comment (empty data), got %+v", ev)
	}

	ev, err = dec.next()
	if err != nil {
		t.Fatalf("next 3: %v", err)
	}
	if ev.id != "01ABD" || string(ev.data) != "line1\nline2" {
		t.Fatalf("event 3 = %+v", ev)
	}

	if _, err := dec.next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestSSE_CRLFAndPartialTail(t *testing.T) {
	raw := "id: X\r\ndata: hello\r\n\r\ndata: incomplete-no-blank\r\n"
	dec := newSSEDecoder(strings.NewReader(raw))

	ev, err := dec.next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if ev.id != "X" || string(ev.data) != "hello" {
		t.Fatalf("ev = %+v", ev)
	}
	if _, err := dec.next(); err != io.EOF {
		t.Fatalf("partial trailing record should yield EOF, got %v", err)
	}
}

func TestSSE_RecordTooLarge(t *testing.T) {
	big := "data: " + strings.Repeat("x", maxEventBytes+10) + "\n\n"
	if _, err := newSSEDecoder(strings.NewReader(big)).next(); err == nil {
		t.Fatal("expected error for oversized record")
	}
}
