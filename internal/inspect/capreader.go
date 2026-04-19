package inspect

import "io"

// capReader wraps an io.Reader, passing all bytes through while capturing
// up to max bytes into an internal buffer. Once the cap is reached the
// remaining bytes flow through uncopied.
type capReader struct {
	src     io.Reader
	max     int
	buf     []byte
	total   int64
	capped  bool
	touched bool
}

func newCapReader(src io.Reader, max int) *capReader {
	if src == nil {
		return &capReader{max: max}
	}
	return &capReader{src: src, max: max}
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.src == nil {
		return 0, io.EOF
	}
	n, err := c.src.Read(p)
	if n > 0 {
		c.touched = true
		c.total += int64(n)
		if !c.capped {
			remaining := c.max - len(c.buf)
			if n <= remaining {
				c.buf = append(c.buf, p[:n]...)
			} else {
				if remaining > 0 {
					c.buf = append(c.buf, p[:remaining]...)
				}
				c.capped = true
			}
		}
	}
	return n, err
}

// Snapshot returns the captured prefix, the total bytes observed, and a
// reason tag appropriate for Record fields.
func (c *capReader) Snapshot() ([]byte, int64, BodyReason) {
	switch {
	case !c.touched || c.total == 0:
		return nil, 0, BodyFull
	case c.capped:
		return c.buf, c.total, BodyTooLarge
	default:
		return c.buf, c.total, BodyFull
	}
}
