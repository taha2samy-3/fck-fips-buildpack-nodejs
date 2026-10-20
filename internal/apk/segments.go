package apk

import (
	"bufio"
	"compress/gzip"
	"errors"
	"hash"
	"io"
)

// segments walks the gzip members an apk file or APKINDEX is made of.
//
// apk signs and checksums the compressed bytes of individual members, so the
// reader has to know exactly which input bytes belong to which member.
// gzip.Reader reads through any io.ByteReader it is given without buffering
// ahead, so feeding it this type lets every consumed byte be hashed and no
// byte of the next member be swallowed.
type segments struct {
	src     *bufio.Reader
	gz      *gzip.Reader
	hashes  []hash.Hash
	pending []byte
}

func newSegments(r io.Reader) *segments {
	return &segments{src: bufio.NewReaderSize(r, 64*1024), pending: make([]byte, 0, 32*1024)}
}

func (s *segments) Read(p []byte) (int, error) {
	s.flush()
	n, err := s.src.Read(p)
	for _, h := range s.hashes {
		h.Write(p[:n])
	}
	return n, err
}

func (s *segments) ReadByte() (byte, error) {
	b, err := s.src.ReadByte()
	if err == nil {
		// flate pulls most of its input one byte at a time; batch the hashing.
		s.pending = append(s.pending, b)
		if len(s.pending) == cap(s.pending) {
			s.flush()
		}
	}
	return b, err
}

func (s *segments) flush() {
	for _, h := range s.hashes {
		h.Write(s.pending)
	}
	s.pending = s.pending[:0]
}

// next starts the following member, hashing its compressed bytes into hs, and
// returns its decompressed content. It returns io.EOF after the last member.
func (s *segments) next(hs ...hash.Hash) (io.Reader, error) {
	if _, err := s.src.Peek(1); errors.Is(err, io.EOF) {
		return nil, io.EOF
	} else if err != nil {
		return nil, err
	}

	s.hashes = hs
	var err error
	if s.gz == nil {
		s.gz, err = gzip.NewReader(s)
	} else {
		err = s.gz.Reset(s)
	}
	if err != nil {
		return nil, err
	}
	s.gz.Multistream(false)
	return s.gz, nil
}

// finish consumes the rest of the current member, including its trailer,
// so that its hashes are complete.
func (s *segments) finish() error {
	if _, err := io.Copy(io.Discard, s.gz); err != nil {
		return err
	}
	s.flush()
	s.hashes = nil
	return nil
}
