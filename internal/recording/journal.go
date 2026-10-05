package recording

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type journalReader struct {
	reader   *bufio.Reader
	header   Recording
	bytes    int
	events   int
	duration int64
}

type recordingOutput struct {
	destination io.Writer
	remaining   int
}

func (w *recordingOutput) Write(data []byte) (int, error) {
	if len(data) > w.remaining {
		return 0, fmt.Errorf("recording exceeds %d byte limit", MaxExpandedBytes)
	}
	n, err := w.destination.Write(data)
	w.remaining -= n
	return n, err
}

// Bound each line before JSON decoding and retain only the current event
func newJournalReader(source io.Reader) (*journalReader, error) {
	r := &journalReader{
		reader: bufio.NewReaderSize(io.LimitReader(source, MaxExpandedBytes+1), MaxJournalLineBytes+1),
	}
	line, err := r.line()
	if err != nil {
		return nil, err
	}
	var h journalHeader
	if err := json.Unmarshal(line, &h); err != nil {
		return nil, err
	}
	if h.Format != JournalFormat {
		return nil, ErrUnsupported
	}
	if h.Recording.Pack != "" || len(h.Recording.Lines) != 0 {
		return nil, fmt.Errorf("invalid journal header")
	}
	if !validSize(h.Recording.Sizes) {
		return nil, fmt.Errorf("invalid terminal size")
	}
	if err := validateMetadata(&h.Recording, 604800000); err != nil {
		return nil, err
	}
	r.header = h.Recording
	r.events = len(h.Recording.Lines)
	for _, event := range h.Recording.Lines {
		r.duration += event.Time
	}
	return r, nil
}

func (r *journalReader) line() ([]byte, error) {
	line, err := r.reader.ReadSlice('\n')
	r.bytes += len(line)
	if r.bytes > MaxExpandedBytes {
		return nil, fmt.Errorf("input exceeds %d byte limit", MaxExpandedBytes)
	}
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > MaxJournalLineBytes+1 {
		return nil, fmt.Errorf("journal entry exceeds %d byte limit", MaxJournalLineBytes)
	}
	return line, err
}

func (r *journalReader) next() (Event, error) {
	line, err := r.line()
	if err != nil {
		// An unfinished final line is omitted just as it is during journal recovery
		return Event{}, err
	}
	var event Event
	if err := json.Unmarshal(line, &event); err != nil {
		return Event{}, err
	}
	r.events++
	if r.events > MaxEvents {
		return Event{}, fmt.Errorf("too many recording events")
	}
	if err := validateEvent(event, &r.duration); err != nil {
		return Event{}, err
	}
	return event, nil
}

// Finalize the journal as independently compressed chunks without retaining all events.
func finalizeJournal(destination io.Writer, source io.Reader) error {
	r, err := newJournalReader(source)
	if err != nil {
		return err
	}
	return writeArchive(destination, r.header, func(write func(Event) error) error {
		for _, event := range r.header.Lines {
			if err := write(event); err != nil {
				return err
			}
		}
		for {
			event, err := r.next()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := write(event); err != nil {
				return err
			}
		}
	})
}
