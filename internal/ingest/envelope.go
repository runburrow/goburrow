package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Envelope is a parsed Sentry envelope.
// Format: https://develop.sentry.dev/sdk/data-model/envelopes/
//
//	{envelope header json}\n
//	{item header json}\n
//	{payload (length bytes if header has "length", else up to \n)}\n
//	...
type Envelope struct {
	Header EnvelopeHeader
	Items  []Item
}

type EnvelopeHeader struct {
	EventID string `json:"event_id"`
	DSN     string `json:"dsn"`
}

type Item struct {
	Type    string
	Payload []byte
}

var ErrMalformedEnvelope = errors.New("malformed envelope")

func ParseEnvelope(body []byte) (*Envelope, error) {
	env := &Envelope{}
	line, rest := nextLine(body)
	if len(bytes.TrimSpace(line)) == 0 {
		return nil, fmt.Errorf("%w: missing header", ErrMalformedEnvelope)
	}
	if err := json.Unmarshal(line, &env.Header); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrMalformedEnvelope, err)
	}

	for len(rest) > 0 {
		var hdrLine []byte
		hdrLine, rest = nextLine(rest)
		if len(bytes.TrimSpace(hdrLine)) == 0 {
			continue // tolerate blank lines / trailing newline
		}
		var ih struct {
			Type   string          `json:"type"`
			Length json.RawMessage `json:"length"`
		}
		if err := json.Unmarshal(hdrLine, &ih); err != nil {
			return nil, fmt.Errorf("%w: item header: %v", ErrMalformedEnvelope, err)
		}
		var payload []byte
		if l := bytes.TrimSpace(ih.Length); len(l) > 0 && string(l) != "null" {
			n, err := strconv.Atoi(string(l))
			if err != nil || n < 0 || n > len(rest) {
				return nil, fmt.Errorf("%w: bad item length", ErrMalformedEnvelope)
			}
			payload, rest = rest[:n], rest[n:]
			// Consume the optional newline terminating the payload.
			if len(rest) > 0 && rest[0] == '\n' {
				rest = rest[1:]
			}
		} else {
			payload, rest = nextLine(rest)
		}
		env.Items = append(env.Items, Item{Type: ih.Type, Payload: payload})
	}
	return env, nil
}

func nextLine(b []byte) (line, rest []byte) {
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		return bytes.TrimSuffix(b[:i], []byte{'\r'}), b[i+1:]
	}
	return b, nil
}
