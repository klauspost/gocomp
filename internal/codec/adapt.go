package codec

import (
	"bytes"
	"io"
	"slices"
)

// appender is an io.Writer that appends into a caller-supplied buffer, so a
// streaming encoder can produce a []byte without an intermediate copy.
type appender struct{ b []byte }

func (a *appender) Write(p []byte) (int, error) {
	a.b = append(a.b, p...)
	return len(p), nil
}

// ReadFrom lets io.Copy fill the buffer directly instead of bouncing through
// its own 32KB staging buffer.
func (a *appender) ReadFrom(r io.Reader) (int64, error) {
	var n int64
	for {
		if cap(a.b)-len(a.b) < 64<<10 {
			a.b = slices.Grow(a.b, 1<<20)
		}
		m, err := r.Read(a.b[len(a.b):cap(a.b)])
		a.b = a.b[:len(a.b)+m]
		n += int64(m)
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
	}
}

// streamEnc adapts a Reset/Write/Close encoder. Reset reuse plus a pre-sized
// destination keeps the timed region allocation-free.
type streamEnc struct {
	a    appender
	rst  func(io.Writer) // Reset, plus re-applying any option that Reset clears
	w    io.Writer       // the encoder
	fast func([]byte) error
	fin  func() error // finishes the frame; the encoder stays reusable
	rel  func()
}

func (s *streamEnc) Encode(dst, src []byte) ([]byte, error) {
	s.a.b = dst[:0]
	s.rst(&s.a)
	if s.fast != nil {
		// EncodeBuffer is the only concurrent path in s2/minlz; a plain Write
		// of a large buffer goes through the sequential encoder.
		if err := s.fast(src); err != nil {
			return nil, err
		}
	} else if _, err := s.w.Write(src); err != nil {
		return nil, err
	}
	if err := s.fin(); err != nil {
		return nil, err
	}
	return s.a.b, nil
}

func (s *streamEnc) Release() {
	if s.rel != nil {
		s.rel()
	}
}

// streamDec adapts a Reset/WriteTo decoder.
type streamDec struct {
	a   appender
	br  bytes.Reader
	rst func(io.Reader) error
	fn  func(w io.Writer) (int64, error) // WriteTo, DecodeConcurrent or io.Copy
	rel func()
}

func (s *streamDec) Decode(dst, src []byte) ([]byte, error) {
	s.a.b = dst[:0]
	s.br.Reset(src)
	if err := s.rst(&s.br); err != nil {
		return nil, err
	}
	if _, err := s.fn(&s.a); err != nil {
		return nil, err
	}
	return s.a.b, nil
}

func (s *streamDec) Release() {
	if s.rel != nil {
		s.rel()
	}
}

// blockFn wraps a one-shot append-style function. It satisfies both Encoder
// and Decoder, since the signature is the same in both directions.
type blockFn struct {
	fn  func(dst, src []byte) ([]byte, error)
	rel func()
}

func (b blockFn) Encode(dst, src []byte) ([]byte, error) { return b.fn(dst, src) }
func (b blockFn) Decode(dst, src []byte) ([]byte, error) { return b.fn(dst, src) }

func (b blockFn) Release() {
	if b.rel != nil {
		b.rel()
	}
}

// nopReset is for decoders whose Reset does not return an error.
func nopReset(f func(io.Reader)) func(io.Reader) error {
	return func(r io.Reader) error { f(r); return nil }
}

// copyTo adapts a plain io.Reader decoder to the streamDec fn signature.
func copyTo(r io.Reader) func(io.Writer) (int64, error) {
	return func(w io.Writer) (int64, error) { return io.Copy(w, r) }
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
