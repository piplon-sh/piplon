package probe

import (
	"bufio"
	"bytes"
	"io"
)

// maxSSELine bounds one server-sent event line; probe replies are tiny.
const maxSSELine = 1 << 20

// readSSE calls fn for every complete server-sent event in r, in order,
// until fn returns false or r ends. A trailing event without its blank line
// is dropped, as the SSE format requires. It returns nil at the end of r.
func readSSE(r io.Reader, fn func(event string, data []byte) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxSSELine)
	var event string
	var data []byte
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case len(line) == 0:
			if event != "" || data != nil {
				if !fn(event, data) {
					return nil
				}
			}
			event, data = "", nil
		case line[0] == ':': // comment
		default:
			field, value, _ := bytes.Cut(line, []byte(":"))
			value = bytes.TrimPrefix(value, []byte(" "))
			switch string(field) {
			case "event":
				event = string(value)
			case "data":
				if data != nil {
					data = append(data, '\n')
				}
				data = append(data, value...) // copies: the scanner reuses line
				if data == nil {
					data = []byte{}
				}
			}
		}
	}
	return sc.Err()
}
