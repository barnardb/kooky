package safari

// Read safari kooky.Cookie.binarycookies files.
// Thanks to https://github.com/as0ler/BinaryCookieReader

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/zellyn/kooky"
	"github.com/zellyn/kooky/internal"
)

type fileHeader struct {
	Magic    [4]byte
	NumPages int32
}

type pageHeader struct {
	Header     [4]byte
	NumCookies int32
}

type cookieHeader struct {
	Size           int32
	Unknown1       int32
	Flags          int32
	Unknown2       int32
	UrlOffset      int32
	NameOffset     int32
	PathOffset     int32
	ValueOffset    int32
	End            [8]byte
	ExpirationDate float64
	CreationDate   float64
}

type safariCookieStore struct {
	internal.DefaultCookieStore
}

var _ kooky.CookieStore = (*safariCookieStore)(nil)

func ReadCookies(filename string, filters ...kooky.Filter) ([]*kooky.Cookie, error) {
	s := &safariCookieStore{}
	s.FileNameStr = filename
	s.BrowserStr = `safari`

	defer s.Close()

	return s.ReadCookies(filters...)
}

func (s *safariCookieStore) ReadCookies(filters ...kooky.Filter) ([]*kooky.Cookie, error) {
	var cookies []*kooky.Cookie
	return cookies, s.VisitCookies(func(cookie *kooky.Cookie, initializeValue kooky.CookieValueInitializer) error {
		// we know the value doesn't need initialization
		if kooky.FilterCookie(cookie, filters...) {
			cookies = append(cookies, cookie)
		}
		return nil
	})
}

func (s *safariCookieStore) VisitCookies(visit kooky.CookieVisitor) error {
	if s == nil {
		return errors.New(`cookie store is nil`)
	}
	if err := s.Open(); err != nil {
		return err
	} else if s.File == nil {
		return errors.New(`file is nil`)
	}

	var header fileHeader
	err := binary.Read(s.File, binary.BigEndian, &header)
	if err != nil {
		return fmt.Errorf("error reading header: %v", err)
	}
	if string(header.Magic[:]) != "cook" {
		return fmt.Errorf("expected first 4 bytes to be %q; got %q", "cook", string(header.Magic[:]))
	}

	pageSizes := make([]int32, header.NumPages)
	if err = binary.Read(s.File, binary.BigEndian, &pageSizes); err != nil {
		return fmt.Errorf("error reading page sizes: %v", err)
	}

	for i, pageSize := range pageSizes {
		if err = readPage(s.File, pageSize, visit); err != nil {
			return fmt.Errorf("error reading page %d: %v", i, err)
		}
	}

	// TODO(zellyn): figure out how the checksum works.
	var checksum [8]byte
	err = binary.Read(s.File, binary.BigEndian, &checksum)
	if err != nil {
		return fmt.Errorf("error reading checksum: %v", err)
	}

	return nil
}

func readPage(f io.Reader, pageSize int32, visit kooky.CookieVisitor) error {
	bb := make([]byte, pageSize)
	if _, err := io.ReadFull(f, bb); err != nil {
		return err
	}
	r := bytes.NewReader(bb)

	var header pageHeader
	if err := binary.Read(r, binary.LittleEndian, &header); err != nil {
		return fmt.Errorf("error reading header: %v", err)
	}
	want := [4]byte{0x00, 0x00, 0x01, 0x00}
	if header.Header != want {
		return fmt.Errorf("expected first 4 bytes of page to be %v; got %v", want, header.Header)
	}

	cookieOffsets := make([]int32, header.NumCookies)
	if err := binary.Read(r, binary.LittleEndian, &cookieOffsets); err != nil {
		return fmt.Errorf("error reading cookie offsets: %v", err)
	}

	for i, cookieOffset := range cookieOffsets {
		r.Seek(int64(cookieOffset), io.SeekStart)
		cookie, err := readCookie(r)
		if err != nil {
			return fmt.Errorf("cookie %d: %v", i, err)
		}
		err = visit(cookie, kooky.CookieValueAlreadyInitialized)
		if err != nil {
			return err
		}
	}

	return nil
}

func readCookie(r io.ReadSeeker) (*kooky.Cookie, error) {
	start, _ := r.Seek(0, io.SeekCurrent)
	var ch cookieHeader
	if err := binary.Read(r, binary.LittleEndian, &ch); err != nil {
		return nil, err
	}

	expiry := safariCookieDate(ch.ExpirationDate)
	creation := safariCookieDate(ch.CreationDate)

	url, err := readString(r, "url", start, ch.UrlOffset)
	if err != nil {
		return nil, err
	}
	name, err := readString(r, "name", start, ch.NameOffset)
	if err != nil {
		return nil, err
	}
	path, err := readString(r, "path", start, ch.PathOffset)
	if err != nil {
		return nil, err
	}
	value, err := readString(r, "value", start, ch.ValueOffset)
	if err != nil {
		return nil, err
	}

	cookie := &kooky.Cookie{}

	cookie.Expires = expiry
	cookie.Creation = creation
	cookie.Name = name
	cookie.Value = value
	cookie.Domain = url
	cookie.Path = path
	cookie.Secure = (ch.Flags & 1) > 0
	cookie.HttpOnly = (ch.Flags & 4) > 0
	return cookie, nil
}

func readString(r io.ReadSeeker, field string, start int64, offset int32) (string, error) {
	if _, err := r.Seek(start+int64(offset), io.SeekStart); err != nil {
		return "", fmt.Errorf("seeking for %q at offset %d", field, offset)
	}
	b := bufio.NewReader(r)
	value, err := b.ReadString(0)
	if err != nil {
		return "", fmt.Errorf("reading for %q at offset %d", field, offset)
	}

	return value[:len(value)-1], nil
}

// safariCookieDate converts double seconds to a time.Time object,
// accounting for the switch to Mac epoch (Jan 1 2001).
func safariCookieDate(floatSecs float64) time.Time {
	seconds, frac := math.Modf(floatSecs)
	return time.Unix(int64(seconds)+978307200, int64(frac*1000000000))
}
