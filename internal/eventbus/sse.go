package eventbus

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Server-Sent Events framing (https://html.spec.whatwg.org/multipage/server-sent-events.html).
// We use three fields: id, event and data. A record ends with a blank
// line. Lines beginning with ":" are comments and are used as heartbeats.

// maxEventBytes bounds a single incoming SSE record so a broken or
// hostile stream cannot make the client allocate without limit.
const maxEventBytes = 1 << 20 // 1 MiB

// writeSSEEvent writes one SSE record. data may contain newlines; each
// line is emitted as its own "data:" field, which the reader rejoins.
func writeSSEEvent(w io.Writer, id, event string, data []byte) error {
	var b bytes.Buffer
	if id != "" {
		fmt.Fprintf(&b, "id: %s\n", id)
	}
	if event != "" {
		fmt.Fprintf(&b, "event: %s\n", event)
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		b.WriteString("data: ")
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	_, err := w.Write(b.Bytes())
	return err
}

// writeSSEComment writes a comment line, used as a keep-alive ping.
func writeSSEComment(w io.Writer, text string) error {
	_, err := fmt.Fprintf(w, ": %s\n\n", text)
	return err
}

// sseEvent is one decoded record. A record with empty data is a
// comment/heartbeat: the client resets its idle timer and emits nothing.
type sseEvent struct {
	id    string
	event string
	data  []byte
}

// sseDecoder reads SSE records from a stream.
type sseDecoder struct {
	r *bufio.Reader
}

func newSSEDecoder(r io.Reader) *sseDecoder {
	return &sseDecoder{r: bufio.NewReaderSize(r, 4096)}
}

// next returns the next record, or an error (io.EOF at a clean end).
func (d *sseDecoder) next() (sseEvent, error) {
	var (
		ev       sseEvent
		dataBuf  bytes.Buffer
		haveData bool
		haveAny  bool
		total    int
	)

	for {
		line, err := d.r.ReadString('\n')
		if err != nil {
			// A partial record at EOF is discarded, matching the spec's
			// "incomplete records are ignored".
			return sseEvent{}, err
		}
		total += len(line)
		if total > maxEventBytes {
			return sseEvent{}, fmt.Errorf("eventbus: SSE record exceeds %d bytes", maxEventBytes)
		}

		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			// End of record.
			if !haveAny && !haveData {
				// Blank line with nothing pending: ignore, keep reading.
				continue
			}
			if haveData {
				// Drop the single trailing newline the accumulation added.
				b := dataBuf.Bytes()
				if len(b) > 0 && b[len(b)-1] == '\n' {
					b = b[:len(b)-1]
				}
				ev.data = append([]byte(nil), b...)
			}
			return ev, nil
		}

		if strings.HasPrefix(line, ":") {
			// Comment (heartbeat). Note it so a comment-only record is
			// still returned, letting the client reset its idle timer.
			haveAny = true
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if found && strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		if !found {
			field, value = line, ""
		}

		switch field {
		case "id":
			ev.id = value
			haveAny = true
		case "event":
			ev.event = value
			haveAny = true
		case "data":
			dataBuf.WriteString(value)
			dataBuf.WriteByte('\n')
			haveData = true
			haveAny = true
		default:
			// Unknown field: ignore per spec.
		}
	}
}
